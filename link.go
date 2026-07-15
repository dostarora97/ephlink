// Package ephlink is a small, CDP-agnostic library for ephemeral, authenticated, peer-to-peer
// links between machines over a Tailscale tailnet (via embedded tsnet). It knows nothing about
// Chrome, CDP, or HTTP — it moves raw TCP bytes between a local service and a named peer.
//
// The API is SYMMETRIC: every machine is just a Node created the same way with Join(). A node
// has capabilities, not a fixed role:
//
//   - Expose(localAddr)      publish a local TCP service on the mesh (others can reach it)
//   - Dial(ctx, "peer:port") open a raw connection to a peer's service by MagicDNS name
//   - Serve(...)             re-present a remote peer's service as a LOCAL listener, optionally
//     passing each connection through a Transform (e.g. an HTTP/L7 rewrite)
//
// Neither side is privileged. A CDP bridge is just: machine A does Expose(chromeCDP); machine B
// does Serve(A, localPort, cdpRewrite). Swap the transform and the same library carries SSH, a
// database port, a dev server — anything TCP.
//
// Lifecycle: nodes are ephemeral (auto-deregister from the tailnet on Close/exit). Close() is
// idempotent and best-effort.
package ephlink

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"tailscale.com/tsnet"
)

// Config configures a node joining the mesh. Symmetric: both sides pass the same shape.
type Config struct {
	// Hostname is this node's MagicDNS name on the tailnet (e.g. "cdp-agent", "cdp-hub").
	Hostname string
	// AuthKey is an ephemeral, pre-authorized (optionally tagged) key. See Mint().
	AuthKey string
	// StateDir holds tsnet node state; empty = a fresh temp dir (fine for ephemeral nodes).
	StateDir string
	// Ephemeral makes the node auto-deregister on Close/exit. Default true; set a pointer to override.
	Ephemeral *bool
	// Logf receives tsnet logs; nil = discard.
	Logf func(format string, args ...any)
}

// Node is a joined mesh participant. Safe for concurrent Dial/Expose/Serve.
type Node struct {
	srv       *tsnet.Server
	listeners []net.Listener
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Join brings this machine onto the mesh as a node. Identical on every side.
func Join(ctx context.Context, cfg Config) (*Node, error) {
	if cfg.AuthKey == "" {
		return nil, fmt.Errorf("ephlink: no auth key (mint one with ephlink.Mint or pass $TS_AUTHKEY)")
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		d, err := os.MkdirTemp("", "ephlink-"+cfg.Hostname+"-*")
		if err != nil {
			return nil, fmt.Errorf("ephlink: state dir: %w", err)
		}
		stateDir = d
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	srv := &tsnet.Server{
		Hostname:  cfg.Hostname,
		AuthKey:   cfg.AuthKey,
		Dir:       stateDir,
		Ephemeral: boolOr(cfg.Ephemeral, true),
		Logf:      logf,
	}
	if _, err := srv.Up(ctx); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("ephlink: joining mesh: %w", err)
	}
	return &Node{srv: srv}, nil
}

// Name returns this node's MagicDNS-reachable hostname and its tailnet IPv4.
func (n *Node) Name() (host, ip4 string) {
	v4, _ := n.srv.TailscaleIPs()
	host = n.srv.Hostname
	if v4.IsValid() {
		ip4 = v4.String()
	}
	return host, ip4
}

// Expose publishes a local TCP service (localAddr, e.g. "127.0.0.1:9222") on the mesh, reachable
// by peers at this node's name on the SAME port. Runs until the node is closed. Non-blocking.
func (n *Node) Expose(localAddr string, port int) error {
	ln, err := n.srv.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("ephlink: listen on mesh :%d: %w", port, err)
	}
	n.listeners = append(n.listeners, ln)
	go acceptAndSplice(ln, localAddr)
	return nil
}

// Dial opens a raw TCP connection to a peer's service by name or IP (e.g. "cdp-agent:9222" or
// "100.x.y.z:9222"). If the host is a name, it is resolved to the peer's tailnet IP via the mesh
// peer list (more robust than relying on tsnet's internal MagicDNS resolver, which can lag for
// freshly-joined ephemeral peers). Retries briefly to allow peer propagation.
func (n *Node) Dial(ctx context.Context, peerHostPort string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(peerHostPort)
	if err != nil {
		return nil, fmt.Errorf("ephlink: bad peer %q: %w", peerHostPort, err)
	}
	// Already an IP → dial straight over the mesh.
	if net.ParseIP(host) != nil {
		return n.srv.Dial(ctx, "tcp", peerHostPort)
	}
	// Resolve the name to a peer tailnet IP, retrying for propagation.
	ip, err := n.resolvePeerIP(ctx, host)
	if err != nil {
		return nil, err
	}
	return n.srv.Dial(ctx, "tcp", net.JoinHostPort(ip, port))
}

// resolvePeerIP maps a peer hostname (short name or FQDN) to its tailnet IPv4 via mesh status.
func (n *Node) resolvePeerIP(ctx context.Context, host string) (string, error) {
	lc, err := n.srv.LocalClient()
	if err != nil {
		return "", fmt.Errorf("ephlink: local client: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, err := lc.Status(ctx)
		if err == nil && st != nil {
			for _, p := range st.Peer {
				name := strings.TrimSuffix(p.DNSName, ".")
				short := p.HostName
				// Prefer an ONLINE peer; skip stale/offline nodes that may share a base name
				// (Tailscale appends -1/-2 on collision, but the base name can still resolve to
				// an offline node otherwise).
				if !p.Online {
					continue
				}
				if name == host || short == host ||
					strings.HasPrefix(name, host+".") || strings.EqualFold(short, host) {
					for _, ip := range p.TailscaleIPs {
						if ip.Is4() {
							return ip.String(), nil
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("ephlink: peer %q not found on mesh (not up yet, or wrong name)", host)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// Serve re-presents a remote peer's service as a LOCAL listener (localListenAddr). Each accepted
// local connection is dialed to peerHostPort over the mesh. If transform is non-nil, it wraps the
// pair instead of a raw byte splice — this is the ONLY extension point an application (e.g. a CDP
// rewrite) needs; ephlink stays protocol-agnostic. Returns the local net.Listener.
func (n *Node) Serve(ctx context.Context, peerHostPort, localListenAddr string, transform Transform) (net.Listener, error) {
	ln, err := net.Listen("tcp", localListenAddr)
	if err != nil {
		return nil, fmt.Errorf("ephlink: local listen %s: %w", localListenAddr, err)
	}
	n.listeners = append(n.listeners, ln)
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				remote, err := n.Dial(ctx, peerHostPort)
				if err != nil {
					_ = local.Close()
					return
				}
				if transform != nil {
					transform(local, remote)
				} else {
					spliceConns(local, remote)
				}
			}()
		}
	}()
	return ln, nil
}

// Transform processes a matched (local, remote) connection pair. nil = raw byte splice.
// An HTTP/L7 rewriter (e.g. CDP) implements this by serving over `local` and proxying to `remote`.
type Transform func(local, remote net.Conn)

// Close tears down all listeners and the node. Ephemeral nodes auto-deregister from the tailnet.
// Idempotent, best-effort.
func (n *Node) Close() error {
	if n == nil {
		return nil
	}
	for _, ln := range n.listeners {
		_ = ln.Close()
	}
	if n.srv != nil {
		_ = n.srv.Close()
	}
	return nil
}

// acceptAndSplice accepts mesh connections and splices each to a local address (Expose side).
func acceptAndSplice(ln net.Listener, localAddr string) {
	for {
		meshConn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			local, err := net.Dial("tcp", localAddr)
			if err != nil {
				_ = meshConn.Close()
				return
			}
			spliceConns(meshConn, local)
		}()
	}
}

// spliceConns copies bytes both directions between two conns until either closes.
func spliceConns(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) { _, _ = io.Copy(dst, src); done <- struct{}{} }
	go cp(a, b)
	go cp(b, a)
	<-done
}
