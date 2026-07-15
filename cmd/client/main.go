// Command client is the operator-side CDP consumer of the ephlink mesh (cmd/client).
//
// It is the "client" end of the paired host/client tools: it runs on the operator's machine,
// joins the ephemeral mesh (ephlink), then re-serves a peer host's Chrome CDP endpoint as a
// LOCAL endpoint (ws://localhost:PORT + /json*) so any CDP client (Playwright, chrome-devtools
// MCP, raw scripts) attaches unchanged. The ONLY CDP-specific logic here is the Chrome-≥94
// `Host` rewrite + `webSocketDebuggerUrl` rewrite — done with stdlib httputil.ReverseProxy whose
// Transport dials the host over the mesh by MagicDNS name.
//
// ephlink carries the transport; client is just its CDP-flavored consumer.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/dostarora97/ephlink"
)

var version = "0.1.0-dev"

func main() {
	var (
		peer      string
		localPort int
		hostname  string
		authKey   string
	)
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Join the ephemeral mesh and re-serve a peer host's Chrome CDP locally.",
		Long: "client joins the ephemeral mesh, dials a peer host by its MagicDNS name, and " +
			"re-serves that host's Chrome DevTools Protocol endpoint at a local address with the " +
			"Chrome Host/webSocketDebuggerUrl rewrite, so any CDP client can attach. Its paired " +
			"tool, `host`, runs on the machine whose Chrome is shared.",
		Example:       "  client --peer cdp-host:9222 --local-port 9333",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key := authKey
			if key == "" {
				key = os.Getenv("TS_AUTHKEY")
			}
			if key == "" {
				return fmt.Errorf("no auth key: pass --authkey (or $TS_AUTHKEY); mint one with the `mint` tool")
			}
			if peer == "" {
				return fmt.Errorf("--peer is required, e.g. --peer cdp-host:9222")
			}
			return run(cmd.Context(), key, hostname, peer, localPort)
		},
	}
	f := cmd.Flags()
	f.StringVar(&peer, "peer", "", "peer host to reach by MagicDNS name:port (e.g. cdp-host:9222)")
	f.IntVar(&localPort, "local-port", 0, "local port to re-serve on (0 = OS picks)")
	f.StringVar(&hostname, "hostname", "cdp-client", "MagicDNS hostname for this node on the mesh")
	f.StringVar(&authKey, "authkey", "", "ephemeral mesh auth key (from the `mint` tool / $TS_AUTHKEY)")

	if err := fang.Execute(context.Background(), cmd, fang.WithVersion(version), fang.WithNotifySignal(os.Interrupt)); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, authKey, hostname, peer string, localPort int) error {
	// Join the mesh (ephlink handles all transport/identity/lifecycle).
	node, err := ephlink.Join(ctx, ephlink.Config{Hostname: hostname, AuthKey: authKey})
	if err != nil {
		return err
	}
	defer node.Close()
	fmt.Fprintf(os.Stderr, "joined mesh as %q; dialing peer %s over MagicDNS…\n", hostname, peer)

	// The ONLY CDP-specific logic: a reverse proxy whose Transport dials the peer over the mesh,
	// with the Host + webSocketDebuggerUrl rewrites. ReverseProxy handles WebSocket upgrades.
	proxy := cdpReverseProxy(ctx, node, peer)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("local listen: %w", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	local := fmt.Sprintf("127.0.0.1:%d", addr.Port)
	fmt.Fprintf(os.Stderr, "CDP available locally at http://%s\n", local)
	fmt.Fprintf(os.Stderr, "  Playwright: chromium.connectOverCDP(\"http://%s\")\n", local)
	fmt.Fprintf(os.Stderr, "  discovery:  curl http://%s/json/version\n", local)

	srv := &http.Server{Handler: withLocalAuthority(proxy, local, peer)}
	go func() { _ = srv.Serve(ln) }()

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nstopping…")
	_ = srv.Close()
	return nil
}

// cdpReverseProxy builds the reverse proxy: rewrites Host to localhost (Chrome ≥94), dials the
// peer over the ephlink mesh, and rewrites webSocketDebuggerUrl in JSON discovery responses.
func cdpReverseProxy(ctx context.Context, node *ephlink.Node, peer string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "http"
			r.Out.URL.Host = peer
			r.Out.Host = "localhost" // Chrome ≥94: Host must be localhost/IP
		},
		Transport: &http.Transport{
			// Dial the peer over the mesh (MagicDNS name), regardless of the URL host.
			DialContext: func(dctx context.Context, _, _ string) (net.Conn, error) {
				return node.Dial(dctx, peer)
			},
		},
		ModifyResponse: rewriteDiscoveryURLs, // set localAuthority via context (see withLocalAuthority)
	}
}

type ctxKey string

const localAuthorityKey ctxKey = "localAuthority"

// withLocalAuthority injects the local authority + peer into the request context so
// ModifyResponse can rewrite absolute upstream URLs to point back at us.
func withLocalAuthority(h http.Handler, localAuthority, peer string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), localAuthorityKey, [2]string{localAuthority, peer})
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rewriteDiscoveryURLs rewrites webSocketDebuggerUrl/devtoolsFrontendUrl in /json* responses so
// clients' follow-up WebSocket comes back through this proxy, not the (unreachable) peer authority.
func rewriteDiscoveryURLs(resp *http.Response) error {
	v, _ := resp.Request.Context().Value(localAuthorityKey).([2]string)
	localAuthority, peer := v[0], v[1]
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") && !strings.HasPrefix(resp.Request.URL.Path, "/json") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	s := string(body)
	// Chrome emits webSocketDebuggerUrl using whatever Host it saw. Since we send Host: localhost
	// (no port) upstream, it returns ws://localhost/devtools/... . Rewrite ANY ws authority to our
	// local authority so the client's follow-up WebSocket returns through this proxy.
	for _, up := range []string{
		peer, "localhost:" + portOf(peer), "127.0.0.1:" + portOf(peer), "localhost", "127.0.0.1",
	} {
		if up == "" {
			continue
		}
		s = strings.ReplaceAll(s, "ws://"+up+"/", "ws://"+localAuthority+"/")
		s = strings.ReplaceAll(s, "ws="+up+"/", "ws="+localAuthority+"/")
	}
	resp.Body = io.NopCloser(strings.NewReader(s))
	resp.ContentLength = int64(len(s))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(s)))
	return nil
}

func portOf(hostPort string) string {
	if _, p, err := net.SplitHostPort(hostPort); err == nil {
		return p
	}
	return ""
}
