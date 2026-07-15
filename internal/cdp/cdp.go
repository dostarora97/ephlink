// Package cdp holds the only Chrome-DevTools-Protocol-specific logic in the project: the rewrites
// required to re-present a Chrome CDP endpoint under a different authority.
//
// Two rewrites matter:
//   - Host header → "localhost": Chrome ≥94 rejects a CDP WebSocket whose Host isn't localhost/IP.
//   - webSocketDebuggerUrl in /json* discovery responses → the authority we advertise, so a client's
//     follow-up WebSocket comes back through us rather than to an authority it can't reach.
//
// It provides an http.Handler (a configured httputil.ReverseProxy) that a caller drives over any
// net.Listener — a tailnet listener (host serving on its own MagicDNS name) or a loopback listener
// (local-only mode). The proxy dials the local Chrome per request, so multiple requests and
// WebSocket upgrades work correctly. Everything else (transport, identity, lifecycle) is ephlink's.
package cdp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
)

// Handler returns an http.Handler that reverse-proxies CDP to the Chrome at chromeHostPort
// DialFunc opens a connection to the upstream Chrome CDP endpoint. Two flavors are used:
// a local dialer (net.Dial to 127.0.0.1, for host --local-only) and a mesh dialer
// (ephlink.Node.Dial to a raw host peer, for client re-presenting).
type DialFunc func(ctx context.Context) (net.Conn, error)

// LocalDialer dials a local TCP address (e.g. "127.0.0.1:9222").
func LocalDialer(addr string) DialFunc {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// Handler returns an http.Handler that reverse-proxies CDP to the upstream reached by dial,
// advertising `advertise` (e.g. "cdp-host-alice.<tailnet>.ts.net:9222" or "127.0.0.1:9333") in
// discovery URLs so clients loop back through us. upstreamAuthority is the host:port the upstream
// believes it is (used to recognise/rewrite the ws:// authorities Chrome emits) — for a local Chrome
// pass its address; over the mesh pass the raw host's peer host:port.
func Handler(advertise, upstreamAuthority string, dial DialFunc) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "http"
			r.Out.URL.Host = upstreamAuthority
			r.Out.Host = "localhost" // Chrome ≥94: Host must be localhost/IP
		},
		Transport: &http.Transport{
			// Reach the upstream via the injected dialer, regardless of the (rewritten) URL host.
			DialContext: func(dctx context.Context, _, _ string) (net.Conn, error) {
				return dial(dctx)
			},
		},
		ModifyResponse: rewriteDiscovery(advertise, upstreamAuthority),
	}
}

// rewriteDiscovery rewrites ws:// authorities in /json* bodies to the advertised authority.
func rewriteDiscovery(advertise, upstreamAuthority string) func(*http.Response) error {
	return func(resp *http.Response) error {
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
		port := portOf(upstreamAuthority)
		// Chrome emits webSocketDebuggerUrl using whatever Host it saw. Since we send Host: localhost
		// (no port) upstream, it returns ws://localhost/devtools/... . Rewrite ANY ws authority Chrome
		// might use to the authority we advertise, so the client's follow-up WebSocket returns to us.
		for _, up := range []string{
			upstreamAuthority, "localhost:" + port, "127.0.0.1:" + port, "localhost", "127.0.0.1",
		} {
			if up == "" || up == ":" {
				continue
			}
			s = strings.ReplaceAll(s, "ws://"+up+"/", "ws://"+advertise+"/")
			s = strings.ReplaceAll(s, "ws="+up+"/", "ws="+advertise+"/")
		}
		resp.Body = io.NopCloser(strings.NewReader(s))
		resp.ContentLength = int64(len(s))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(s)))
		return nil
	}
}

func portOf(hostPort string) string {
	if _, p, err := net.SplitHostPort(hostPort); err == nil {
		return p
	}
	return ""
}

// Serve drives the CDP handler over ln until ln closes. Advertises `advertise` in discovery URLs,
// reaching the upstream Chrome via dial. upstreamAuthority is the host:port the upstream believes
// it is (for ws:// authority rewriting).
func Serve(ln net.Listener, advertise, upstreamAuthority string, dial DialFunc) error {
	srv := &http.Server{Handler: Handler(advertise, upstreamAuthority, dial)}
	err := srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
