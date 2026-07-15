# client — operator-side control + re-presenter (Go)

The **client** end of the paired host/client tools: runs on the operator's machine. Hosts stay generic (raw CDP expose); `client` owns everything CDP-specific and manages the fleet:

- `client add <name>` — mint an ephemeral key (tagged `tag:ephlink-host`) and print the exact `host` command to run on `<name>`'s machine, plus the `serve-cdp` command to run here.
- `client list` — list ephlink hosts from the Tailscale API (filtered by tag) with online/offline. Stateless — the tailnet is the source of truth, no local database.
- `client remove <name>` — remove a host's node (`cdp-host-<name>-src`) from the tailnet.
- `client serve-cdp <name>` — join a node `cdp-host-<name>`, dial the raw host over the mesh, apply the CDP `Host`/`webSocketDebuggerUrl` rewrite (via `internal/cdp`), and serve it under that tailnet name. Consumers attach to `http://cdp-host-<name>.<tailnet>.ts.net:<port>`. Long-running (Ctrl+C to stop).

Needs a Tailscale OAuth client secret (`TS_OAUTH_CLIENT_SECRET`, or `.env`) with `auth_keys` + `devices` scopes.

```sh
go build -o client ./cmd/client

client add alice                                   # prints the host + serve-cdp commands
client serve-cdp alice --peer cdp-host-alice-src:9222
# then: chromium.connectOverCDP("http://cdp-host-alice.<tailnet>.ts.net:9222")

client list
client remove alice
```

The only CDP-specific logic is the Chrome-≥94 `Host` rewrite + `webSocketDebuggerUrl` rewrite in `internal/cdp`, driven over an `ephlink` mesh listener whose upstream is dialed via `ephlink.Node.Dial`. Everything else — transport, identity, lifecycle, fleet listing — is `ephlink` + the Tailscale API.
