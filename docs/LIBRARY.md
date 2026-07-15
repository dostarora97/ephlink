# ephlink — library reference

A small, protocol-agnostic Go library for **ephemeral, authenticated, peer-to-peer links between machines** over a Tailscale tailnet (embedded `tsnet`). It moves raw TCP bytes between a local service and a named peer — it knows nothing about CDP, Chrome, or HTTP.

It's the reusable core of this repo: any project that needs "expose a local port from machine A to machine B, ephemerally and securely, and consume it as a local port on B" can import it directly (`go get github.com/dostarora97/ephlink`). The CDP tooling under `cmd/` is its first consumer.

## Symmetric API

Every machine is just a `Node`, created identically. Roles are capabilities, not types.

```go
node, _ := ephlink.Join(ctx, ephlink.Config{Hostname: "myhost", AuthKey: key})
defer node.Close() // ephemeral: auto-deregisters from the tailnet

// publish a local service on the mesh (raw)
node.Expose("127.0.0.1:9222", 9222)

// reach a peer's service by MagicDNS name
conn, _ := node.Dial(ctx, "otherhost:9222")

// re-present a peer's service as a LOCAL listener, optionally transforming the stream
ln, _ := node.Serve(ctx, "otherhost:9222", "127.0.0.1:9333", nil /* or a Transform */)

// serve a local service ON THE MESH under this node's own name (optionally transformed)
node.ServeOnMesh(ctx, 9222, "127.0.0.1:9222", nil /* or a Transform */)

// or get the raw mesh listener yourself and drive any server (http.Server, etc.) over it
meshLn, _ := node.ListenOnMesh(9222)
```

- `Transform func(local, remote net.Conn)` is the ONLY per-connection extension point — nil = raw byte splice; an L7 rewriter (e.g. CDP's Host/webSocketDebuggerUrl fix) implements it by serving over `local` and proxying to `remote`.
- `ListenOnMesh(port)` returns a raw `net.Listener` on this node's tailnet name — use it for L7 protocols (HTTP/CDP) that need many requests + WebSocket upgrades per connection.
- `ServeOnMesh(port, target, transform)` is the mesh-side mirror of `Serve` (listen on the mesh, dial a target, optional transform) — for simple per-conn relays.
- `Mint(ctx, MintOptions{OAuthSecret, Tags, Expiry})` mints an ephemeral tagged key via a Tailscale OAuth client (operator side; secret never leaves the minter). `ListDevices` / `DeleteDevice` read/manage the tailnet via the same OAuth client — so the tailnet itself is your fleet registry (no local database).

## Reuse

CDP is just one consumer: a machine `Expose`s Chrome's CDP port raw; another `ListenOnMesh` + an HTTP server applies a CDP transform and re-presents it under its own tailnet name. Swap the transform (or use nil) to carry SSH, a database port, a dev server — anything TCP.
