# ephlink — library reference

A small, protocol-agnostic Go library for **ephemeral, authenticated, peer-to-peer links between machines** over a Tailscale tailnet (embedded `tsnet`). It moves raw TCP bytes between a local service and a named peer — it knows nothing about CDP, Chrome, or HTTP.

It's the reusable core of this repo: any project that needs "expose a local port from machine A to machine B, ephemerally and securely, and consume it as a local port on B" can import it directly (`go get github.com/dostarora97/ephlink`). The CDP tooling under `cmd/` is its first consumer.

## Symmetric API

Every machine is just a `Node`, created identically. Roles are capabilities, not types.

```go
node, _ := ephlink.Join(ctx, ephlink.Config{Hostname: "myhost", AuthKey: key})
defer node.Close() // ephemeral: auto-deregisters from the tailnet

// publish a local service on the mesh
node.Expose("127.0.0.1:9222", 9222)

// reach a peer's service by MagicDNS name
conn, _ := node.Dial(ctx, "otherhost:9222")

// re-present a peer's service as a LOCAL listener, optionally transforming the stream
ln, _ := node.Serve(ctx, "otherhost:9222", "127.0.0.1:9333", nil /* or a Transform */)
```

- `Transform func(local, remote net.Conn)` is the ONLY extension point — nil = raw byte splice; an L7 rewriter (e.g. CDP's Host/webSocketDebuggerUrl fix) implements it by serving over `local` and proxying to `remote`.
- `Mint(ctx, MintOptions{OAuthSecret, Tags, Expiry})` mints an ephemeral tagged key via a Tailscale OAuth client (operator side; secret never leaves the minter).

## Reuse

CDP is just one consumer: machine A `Expose`s Chrome's CDP port; machine B `Serve`s it locally with a CDP transform. Swap the transform (or use nil) to carry SSH, a database port, a dev server — anything TCP.
