# host — Chrome-side ephlink consumer (Go)

The **host** end of the paired host/client tools: the self-contained binary the user runs on the machine whose Chrome is shared (and can delete anytime). It stays **generic** — on the mesh it does a *raw* expose of Chrome's CDP port and knows nothing about CDP; the operator's `client` applies the CDP rewrite. It:

1. Shows a **consent gate** (huh) — generic, honest disclosure of scope + TTL + how to stop.
2. Launches Chrome with a **fresh temp profile** + CDP port (auto-detects Chrome per-OS; fail-loud if absent, or if the CDP port is already held — it launches its own Chrome and cannot attach to an already-running one; `chrome://inspect` can't enable a debug port on a live browser).
3. **Joins the ephemeral mesh** via `ephlink.Join` and **raw-exposes** the CDP port (`ephlink.Expose`). With `--local-only` it instead serves *rewritten* CDP on loopback (via `internal/cdp`) for a zero-setup single-machine flow.
4. Idempotent **teardown** on quit/Ctrl+C: kills Chrome, removes the temp profile, ephemeral node auto-deregisters.

## Build / run

Built from the repo root (single module):

```sh
go build -o host ./cmd/host
go build -o mint ./cmd/mint      # operator-side key minter (thin CLI over ephlink.Mint)

# usually you get the exact command from `client add <name>`; by hand:
export TS_OAUTH_CLIENT_SECRET=tskey-client-...
KEY=$(./mint)
./host --authkey "$KEY" --hostname cdp-host-alice-src --operator ops

# local testing only (no mesh; host serves rewritten CDP itself):
./host --yes --headless --local-only
```

Flags: `--authkey` (`$TS_AUTHKEY`), `--hostname`, `--cdp-port`, `--operator`, `--ttl`, `--headless`, `--chrome-path`, `--active`, `--real-profile` (not implemented), `--yes`, `--local-only`.

## Layout
- `cmd/host/main.go` — cobra/fang CLI + orchestration (consent → launch → raw Expose / local cdp.Serve → teardown).
- `internal/chrome/` — per-OS Chrome discovery + launch (temp profile) + teardown.
- `internal/consent/` — the consent gate (huh), generic copy + token-capture disclosure.
- `internal/cdp/` — the CDP Host/webSocketDebuggerUrl rewrite (used by `--local-only`; the mesh path leaves it to `client`).

Transport lives in the reusable `ephlink` root package (imported as `github.com/dostarora97/ephlink`).
