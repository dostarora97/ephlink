# Tailscale one-time setup (operator side)

`ephlink` nodes join your tailnet with a short-lived ephemeral key minted by the `mint` tool via an OAuth client. Do this once in the Tailscale admin console. The tags are **generic** (`ephlink-*`) because ephlink is reusable beyond CDP — any consumer shares the same trust model.

## 1. Tailnet policy — tags

Add to your policy file (Access controls). `tag:ephlink-client` = operator/consumer nodes; `tag:ephlink-host` = the ephemeral nodes that expose a local service.

```json
{
  "tagOwners": {
    "tag:ephlink-host":   ["tag:ephlink-client"],
    "tag:ephlink-client": ["autogroup:admin"]
  }
}
```

Notes:
- `tag:ephlink-host` is owned by `tag:ephlink-client` so the OAuth client (which authenticates as the client tag) may mint keys carrying it.
- Optional least-privilege grant (optional hardening): scope host↔client to a specific port, e.g. `{ "grants": [ { "src": ["tag:ephlink-client"], "dst": ["tag:ephlink-host"], "ip": ["tcp:9222"] } ] }`. The default "allow" ACL already permits the traffic; add this only when you want strict scoping.

## 2. OAuth client (for minting ephemeral keys)

Settings → OAuth clients (a.k.a. Trust credentials) → Generate:
- Scope: **`auth_keys`** (write).
- Tag: **`tag:ephlink-host`** (so it may issue keys for that tag).
- Copy the client secret (`tskey-client-...`) — keep it operator-side only.

## 3. Mint a key + run

```sh
export TS_OAUTH_CLIENT_SECRET=tskey-client-xxxxx
KEY=$(mint --expiry 30m)              # defaults to --tag tag:ephlink-host

# host side (embedded tsnet — no system Tailscale needed on the machine):
host --authkey "$KEY" --operator ops  # joins the mesh, exposes Chrome CDP, prints its peer name

# operator side:
client --peer cdp-host:9222 --local-port 9333 --authkey "$KEY2"
```

Each node joins as ephemeral (auto-deregisters on exit). `client` dials the host by its MagicDNS name and re-serves CDP locally for any CDP client.
