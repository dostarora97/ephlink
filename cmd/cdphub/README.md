# cdphub — operator-side CDP consumer (Go)

Joins the ephemeral mesh (`ephlink`), dials a peer agent by its MagicDNS name, and re-serves that agent's Chrome CDP endpoint at a local address (`ws://localhost:PORT` + `/json*`) so any CDP client (Playwright, chrome-devtools MCP, raw scripts) attaches unchanged.

The only CDP-specific logic is the Chrome-≥94 `Host` rewrite + `webSocketDebuggerUrl` rewrite, done with stdlib `httputil.ReverseProxy` whose transport is `ephlink.Node.Dial`. Everything else — transport, identity, lifecycle — is `ephlink`.

```sh
go build -o cdphub ./cmd/cdphub
./cdphub --peer cdp-agent:9222 --local-port 9333 --authkey "$KEY"
# then: chromium.connectOverCDP("http://127.0.0.1:9333")
```

Flags: `--peer` (MagicDNS name:port), `--local-port`, `--hostname`, `--authkey` (`$TS_AUTHKEY`).
