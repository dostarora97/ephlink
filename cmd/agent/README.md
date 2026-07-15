# agent — Chrome-side ephlink consumer (Go)

Self-contained binary the user runs (and can delete anytime). It:

1. Shows a **consent gate** (huh) — generic, honest disclosure of scope + TTL + how to stop.
2. Launches Chrome with a **fresh temp profile** + CDP port (auto-detects Chrome per-OS; fail-loud if absent, or if the CDP port is already held — it launches its own Chrome and cannot attach to an already-running one; `chrome://inspect` can't enable a debug port on a live browser).
3. **Joins the ephemeral mesh** via `ephlink.Join` + `Expose` (embedded tsnet — no system Tailscale needed).
4. Idempotent **teardown** on quit/Ctrl+C: kills Chrome, removes the temp profile, ephemeral node auto-deregisters.

## Build / run

Built from the repo root (single module):

```sh
go build -o agent ./cmd/agent
go build -o mint  ./cmd/mint     # operator-side key minter (thin CLI over ephlink.Mint)

# mint a key, then join the mesh + expose Chrome:
export TS_OAUTH_CLIENT_SECRET=tskey-client-...
KEY=$(./mint)
./agent --authkey "$KEY" --operator ops

# local testing only (no mesh):
./agent --yes --headless --local-only
```

Flags: `--authkey` (`$TS_AUTHKEY`), `--operator`, `--ttl`, `--cdp-port`, `--hostname`, `--headless`, `--chrome-path`, `--active`, `--yes`, `--local-only`.

## Layout
- `cmd/agent/main.go` — cobra/fang CLI + orchestration (consent → launch → ephlink.Expose → teardown).
- `internal/chrome/` — per-OS Chrome discovery + launch (temp profile) + teardown.
- `internal/consent/` — the consent gate (huh), generic copy + token-capture disclosure.
- `cmd/mint/` — operator-side ephemeral key minter (ephlink.Mint).

Transport lives in the reusable `ephlink` root package (imported as `github.com/dostarora97/ephlink`).
