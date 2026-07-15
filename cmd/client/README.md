# client — operator-side CDP consumer (Go)

The **client** end of the paired host/client tools: runs on the operator's machine. Joins the ephemeral mesh (`ephlink`), dials a peer host by its MagicDNS name, and re-serves that host's Chrome CDP endpoint at a local address (`ws://localhost:PORT` + `/json*`) so any CDP client (Playwright, chrome-devtools MCP, raw scripts) attaches unchanged.

The only CDP-specific logic is the Chrome-≥94 `Host` rewrite + `webSocketDebuggerUrl` rewrite, done with stdlib `httputil.ReverseProxy` whose transport is `ephlink.Node.Dial`. Everything else — transport, identity, lifecycle — is `ephlink`.

```sh
go build -o client ./cmd/client
./client --peer cdp-host:9222 --local-port 9333 --authkey "$KEY"
# then: chromium.connectOverCDP("http://127.0.0.1:9333")
```

Flags: `--peer` (MagicDNS name:port), `--local-port`, `--hostname`, `--authkey` (`$TS_AUTHKEY`).
