# Tailscale one-time setup (operator side)

`ephlink` nodes join your tailnet with a short-lived ephemeral key minted by the `mint` tool via an OAuth client. Do this once in the Tailscale admin console. The tags are **generic** (`ephlink-*`) because ephlink is reusable beyond CDP — any consumer shares the same trust model.

## 1. Tailnet policy — tags

Add to your policy file (Access controls). `tag:ephlink-hub` = operator/consumer nodes; `tag:ephlink-agent` = the ephemeral nodes that expose a local service.

```json
{
  "tagOwners": {
    "tag:ephlink-agent": ["tag:ephlink-hub"],
    "tag:ephlink-hub":   ["autogroup:admin"]
  }
}
```

Notes:
- `tag:ephlink-agent` is owned by `tag:ephlink-hub` so the OAuth client (which authenticates as the hub tag) may mint keys carrying it.
- Optional least-privilege grant (optional hardening): scope agent↔hub to a specific port, e.g. `{ "grants": [ { "src": ["tag:ephlink-hub"], "dst": ["tag:ephlink-agent"], "ip": ["tcp:9222"] } ] }`. The default "allow" ACL already permits the traffic; add this only when you want strict scoping.

## 2. OAuth client (for minting ephemeral keys)

Settings → OAuth clients (a.k.a. Trust credentials) → Generate:
- Scope: **`auth_keys`** (write).
- Tag: **`tag:ephlink-agent`** (so it may issue keys for that tag).
- Copy the client secret (`tskey-client-...`) — keep it operator-side only.

## 3. Mint a key + run

```sh
export TS_OAUTH_CLIENT_SECRET=tskey-client-xxxxx
KEY=$(mint --expiry 30m)              # defaults to --tag tag:ephlink-agent

# agent side (embedded tsnet — no system Tailscale needed on the machine):
agent --authkey "$KEY" --operator ops # joins the mesh, exposes Chrome CDP, prints its peer name

# operator side:
cdphub --peer cdp-agent:9222 --local-port 9333 --authkey "$KEY2"
```

Each node joins as ephemeral (auto-deregisters on exit). `cdphub` dials the agent by its MagicDNS name and re-serves CDP locally for any client.
