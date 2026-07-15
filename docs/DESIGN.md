# Design & decision history

A secure, ephemeral capability to connect to a live browser's real Chrome over CDP: passively ingest everything (network / console / events / storage / screencast) and actively drive it, re-presented locally so ANY CDP client attaches (scripts, Playwright, chrome-devtools MCP, an LLM). This document records the design decisions and their reasoning — the trade-offs made, alternatives rejected, and how the approach evolved.

## Current direction (summary)

- **Substrate:** Tailscale (WireGuard mesh, DERP fallback, ephemeral auth keys).
- **Transport library:** `ephlink` — Go + embedded `tsnet`; the reusable, CDP-agnostic core.
- **Consumers:** `agent` (Go, Chrome-side) and `cdphub` (Go, operator-side). Both are thin consumers of `ephlink`.
- **Seam:** everything re-presented as a local CDP endpoint (`ws://localhost:PORT` + `Host: localhost` rewrite for Chrome ≥94), so any CDP client attaches unchanged.

## Deferred hardening (gates broad / untrusted-user distribution)

These were consciously deferred; each is safe to defer only under the "supervised, internal, consented" premise (see *Trust model*), and each gates shipping to non-technical / untrusted users:

- [ ] **D10c** — Live "connected / being-controlled" indicator on the agent (+ a separate consent step for active control vs. passive observe, revisiting the two-tier gate in D5).
- [ ] **D11c** — Signed + notarized binaries (needs paid Apple / Windows code-signing certs).
- [ ] **D13** — Audit trail (session-level minimum; action-level — incl. `Runtime.evaluate` — ideal).
- [ ] **D6** — Provisioning endpoint (pairing-token → ephemeral key) replacing the raw-key handoff.
- [ ] **D9** — Capture riders enforced: encrypt-at-rest, retention/purge, consent discloses token capture.
- [ ] **D14c** — Loopback integration test as a correctness gate.
- [ ] **D5 Layer-2** — Formal authorization/consent process (out-of-tool).
- [ ] **D7** — Tightened least-privilege ACL grant (agent reachable only by the hub, only on the CDP port).

---

## Decisions

### D1 — Purpose & lifespan

- **Now:** a recurring internal support tool (team-facing).
- **Eventually (committed direction):** product-grade / potentially shipped to a broader audience (other support engineers, possibly customer-facing self-serve).
- **Design consequence:** build to the near-term *scope* (don't gold-plate features), but make no architectural choice that the shipped phase would force a rewrite of. Treated as load-bearing from day one:
  - Clean seam between transport and the CDP layer, so the substrate can change.
  - Security posture must be product-grade even while the feature set is minimal (customer-touching + live tokens ⇒ can't "add security later").
  - Consent + audit designed in, not bolted on.
  - Versioned protocol between the two sides (so shipped agents interop across versions).
  - Licensing/telemetry/branding deferred — but code layout should isolate them.
- **Explicitly deferred:** notarization/signing at scale, legal review, self-serve UX, multi-tenant relay, SSO integration.

### D2 — Trust model / operator

- **Ceiling operator:** a non-technical **end-user** — the agent must be runnable by someone following simple instructions. This subsumes supervised/technical operators, so we design to the hardest case and get the easier ones for free.
- **Trust posture:** **mutually untrusted**, but via *modern sane defaults*, NOT a bespoke security apparatus. Guiding rule: prefer boring, well-audited primitives over custom crypto/validation. What "sane default" means concretely (so it isn't relitigated per component):
  - **Protect the user from the operator:** temp profile default, ACL-scoped to one port, explicit consent, all operator-initiated actions auditable, nothing persistent.
  - **Protect the operator from a hostile/compromised agent:** treat inbound CDP as untrusted input — don't `eval`/execute agent-supplied strings on the operator host; cap message sizes; validate JSON shape at the boundary; run least-privilege. Do NOT build custom attestation, agent binary remote-verification, or anomaly detection — premature. A signed binary (later) covers "is this the real agent."
  - **No premature overengineering** is itself a first-class constraint: when the choice is between a "modern default that's 90% as safe" and a "custom thing that's 100%," take the default.
- **First realistic operator:** likely supervised/technical in early use, but no code path assumes it — the non-technical end-user is the design target.

### D3 — Transport substrate — Tailscale

- **Chosen:** Tailscale — `tsnet` embedded in the Go binaries; WireGuard e2e; ephemeral, single-use, pre-authorized auth keys; ACL-scoped nodes.
- **Design against vanilla Tailscale**, not any specific org's account/features. A corporate tailnet may host control in practice, but no design dependency on org-specific SSO/ACL tooling/tenant config. Keeps it portable + product-ready.
- **Why over Cloudflare Tunnel:** WireGuard e2e ⇒ no third party (incl. the relay) can read CDP/tokens; best-in-class NAT traversal; ephemeral nodes auto-deregister (matches "kill anytime" natively). No custom e2e layer needed.
- **Known tension accepted + mitigation:**
  - UDP/WireGuard blocked on some corporate networks → Tailscale auto-falls back to DERP over TCP 443.
  - "User joins a vendor tailnet" ask → ephemeral node + tight ACL (agent tag can reach only the hub node, only on the CDP port) + clear consent copy. It's a single-purpose, single-peer, expiring node, not a general tailnet member.
- **Not built:** Cloudflare / WebRTC (avoid overengineering). The consumer layer stays transport-agnostic internally regardless.

### D4 — Chrome profile — fresh temp by default

- **Fresh temp profile = hard default.** The agent spawns Chrome with a throwaway `--user-data-dir`; the user signs in fresh inside it. Smallest blast radius, clean teardown (delete the temp dir), matches the ephemeral theme.
- **Copy of the real profile = explicit, consent-gated opt-in** (for state-dependent bugs). Copies the live profile dir → launches Chrome on the copy (original untouched).
  - **Acknowledged trade-off:** this necessarily flows the user's real cookies/tokens/session through the CDP stream. Transport is WireGuard-e2e (no third party), but **the operator can see them** — unavoidable if we want their real session. Consent copy must state this plainly. Mitigations: opt-in only, redaction options at capture, short TTL.
  - Technical note: copying a live profile while Chrome holds locks is flaky → close/relaunch or copy with lock handling.
- **Remote debugging is guaranteed on, not assumed.** Because the agent launches its own Chrome with `--remote-debugging-port`, the debug port is always enabled for the built path — the `chrome://inspect/#remote-debugging` toggles are irrelevant here. A running Chrome the tool did NOT launch cannot be retro-enabled: `chrome://inspect` only *discovers/forwards* to targets that already expose a port (e.g. remote Android/other-host targets); it does not turn on a debug port for the browser you are viewing. This is the fundamental reason the "attach to running Chrome" case below is hard. The agent preflights the port (see D15) so a port already held by another/your-own Chrome fails loud immediately with a clear message, rather than surfacing as a confusing timeout.
- **Attach to an already-running real Chrome = roadmap, not built.** Why it's hard: a normally-launched Chrome has no debug port; modern Chrome can't enable it on a running instance (see above); relaunching kills the user's tabs; maximal blast radius. What would make it viable later: a browser-extension companion, or newer Chrome remote-debug affordances, or OS-level automation.

### D5 — Consent & authorization

- **In-tool consent (runtime):**
  - **Floor = explicit one-time gate:** before anything is exposed, require an active confirmation and show exactly what is exposed, to whom, the duration, and that quitting stops everything.
  - **Escalate for real-profile mode AND active control:** a persistent visible indicator while connected (the user always knows), and an extra prompt when real-profile copy or operator-driven control is requested. Rationale: mirrors reputable remote-support tools (TeamViewer/Zoom show a persistent "you are being controlled" state). *(The persistent indicator is currently deferred — see Deferred hardening.)*
- **Out-of-tool authorization process: deferred.** Sane near-term default: a lightweight written record ("user consented to a live browser session on <date>") + an audit record the tool emits. Formal IT/legal sign-off + second-approver flow is a later concern; it blocks no architecture.

### D6 — Handshake / connection object

- **Direction: operator-generated.** The operator mints the credential (holds the Tailscale API creds); the user just runs the agent with a key. Fits the non-technical ceiling.
- **Phasing:**
  - **Now:** the operator mints a raw ephemeral, single-use, pre-authorized, tagged, short-TTL auth key and passes it to the user; the agent joins with it directly.
    - Caveat (accepted for supervised use only): a raw auth key is a bearer credential; if leaked before use it could authorize a tailnet join. Mitigated by single-use (consumed on first join), short TTL, and tag scoping (can only reach the hub's CDP port). Deliver over a private channel; treat as a secret.
  - **Later (committed):** the operator mints a short opaque **pairing token**; the agent redeems it at a minimal **provisioning endpoint** that mints the real ephemeral key server-side. The sensitive key never transits human channels; the endpoint becomes the control point for consent-record, audit, TTL, and revocation.
- **Design invariant that makes the phasing free:** the agent takes an **opaque key/token** — the agent's code and UX are identical across phases; only the operator/provisioning side changes.
- **Not chosen:** user-generated (would need API creds on the user's side — wrong); PIN rendezvous (more infra than needed).

### D7 — ACL scoping & least privilege

Uses Tailscale **grants** (current model, not legacy `acls`). Generic, CDP-agnostic tags — the tags name the trust boundary, not the app, so any ephlink consumer shares them:

```json
{
  "tagOwners": {
    "tag:ephlink-agent": ["tag:ephlink-hub"],
    "tag:ephlink-hub":   ["autogroup:admin"]
  }
}
```

- **Connection model: hub → agent.** Chrome serves the debug port on the agent side; the hub connects into it (the natural CDP model). The agent's `tsnet` binds the CDP port to the tailnet so only the hub tag can reach it. The ephemeral key is minted with the agent tag.
  - **Risk + fallback:** some agent-side networks are egress-only and may block inbound even over the mesh → if that happens in the field, fall back to agent-dials-hub (agent opens the connection outbound; hub becomes the rendezvous). The transport-agnostic seam makes this swap local.
- **Static tag-scoped policy** (not dynamic per-session grants): per-session isolation comes from ephemeral + tagged nodes, not from rewriting ACLs each session. Dynamic per-session grants = premature overengineering.
- The strict agent→hub-only grant is currently optional (the default "allow" ACL already permits the traffic); it's listed under Deferred hardening.

### D8 — The local endpoint (the generic seam)

- **The core deliverable:** re-serve the remote Chrome's CDP as a local `ws://localhost:<port>` plus the `/json`, `/json/version` HTTP discovery endpoints, speaking verbatim CDP, with the `Host: localhost` rewrite (Chrome ≥94 rejects a CDP WS whose Host isn't localhost/IP). This is the seam that makes "connect anything" true, and it stays transport-agnostic (the fallback swaps sit below it).
- **First-class consumers, both validated:** Playwright `connectOverCDP(...)` (scripted) and the chrome-devtools MCP (the "hand to an LLM" path). Raw `chrome-remote-interface` works for free.
- **Fast-follow, designed but not day-one:** always-on passive capture (HAR + console + `Runtime.exceptionThrown` + screencast) alongside whatever client attaches; a session manager; a higher-level MCP over the endpoint.

### D9 — Capture scope & storage

- **Capture everything raw** (chosen): full HAR incl. bodies + all headers (`Cookie`/`Authorization`/`Set-Cookie`), cookies, localStorage, IndexedDB, console, exceptions, screencast. Max fidelity, no redaction by default.
- **Stored locally** on the operator's machine (per-session dir). No central store for now.
- **Riders (recommended sane defaults):** encrypt-at-rest; short default retention + one-command purge; consent copy discloses that a full recording incl. session tokens is stored operator-side.
- **Acknowledged:** every raw capture is a live-credential bearer artifact (replayable until token expiry). Accepted for fidelity; the riders bound the liability without reducing fidelity. A `--redact` flag is a future option for customer-facing use.

### D10 — Active-control policy

- **One consent covers both** passive observe + active drive (simpler; fine for supervised/technical early operators).
- **Unrestricted CDP**, incl. `Runtime.evaluate` (arbitrary JS in the page). No verb allowlist/bounding — it'd be leaky and premature; safety comes from consent (+ audit), not from blocking verbs.
- **No live control/connection indicator for now.**
  - **Tension flagged (conscious deferral, not dropped):** this contradicts the escalation/indicator from *Consent* and the non-technical-ceiling premise from *Trust model* (the user should always know they're connected/controlled).
  - **Resolution:** acceptable while the operator is supervised/technical; a hard blocker for shipping to non-technical users — the indicator + separate active-control step must be reinstated before then. Recorded, not "never."

### D12 — Teardown / kill-switch

- **Support all stop triggers.** Primary: user-quit (Ctrl-C / close / Stop) and hub-disconnect. TTL-expiry is the safety net. Also handle network drop. Note: deleting the binary mid-run does NOT reliably kill a running process; the real kill is quit/Ctrl-C, which is made bulletproof.
- **On any stop, run the full cleanup idempotently:** kill spawned Chrome; delete the temp profile/copy; drop `tsnet` (ephemeral node auto-deregisters); close the CDP port.
  - **Crash-safe:** an ungraceful death still auto-deregisters the ephemeral node (Tailscale handles it); orphaned temp profiles get cleaned on next launch.
  - **Captures survive teardown** — governed by retention/purge, not by teardown.
- **No separate panic button:** quit/Ctrl-C already triggers full idempotent teardown. Design principle: exactly one obvious way to stop, and it fully cleans up.

### D13 — Audit / observability of the bridge

- **None for now** (no session- or action-level self-logging, no audit store).
- **Stacked-risk flag (recorded, conscious deferral):** the current build combines *unrestricted control* + *raw token capture* + *no live indicator* + *no audit*. Together, an operator can do anything to a session, see all tokens, with no record of who did what. Defensible ONLY under the supervised/technical/internal/consented premise.
- **Hardening gate (the hardest one):** before non-technical / shipped use, add at least session-level audit (start/stop, operator identity, consent timestamp, target) and ideally action-level (log of operator-initiated CDP commands, esp. `Runtime.evaluate`). Local append-only first → central/tamper-evident later.

### D11 — Packaging / OS / signing

- **Form factor:** a double-clickable wrapper over the bare Go binary (no terminal required for the non-technical ceiling), showing the consent gate + status. A GUI tray is later polish (and would carry the deferred connection indicator).
- **OS order:** macOS → Linux → Windows. Build-order-agnostic (Go cross-compiles to one static binary per OS); ordering is testing/priority only. macOS first because dev/test happens there; Windows matters for real users but can come once the mechanism is proven.
- **Signing:** unsigned for now → signed + notarized as a gate for shipped use. Unsigned means an OS "unidentified developer" warning — acceptable for supervised/internal; a hard blocker for the non-technical ceiling. GoReleaser is wired for signed cross-builds (config present, disabled until certs exist).

### D14 — Repo / layout / tests

- **Single Go module** (`github.com/dostarora97/ephlink`): the `ephlink` library is the root package (importable on its own via `go get`), the three binaries live under `cmd/agent`, `cmd/cdphub`, `cmd/mint`, and their support code under `internal/`. One `go.mod`, no workspace or `replace` directives — the standard product-scale Go layout. (An earlier iteration used three separate modules tied by a `go.work` workspace + `replace`; collapsed to one module since a single module already exposes the library cleanly while shipping the binaries together. A module can always be split back out later if a consumer needs `ephlink` versioned independently — the reverse merge is the same effort, so start unified.)
- **Tests deferred** for the initial build (validation was manual: drive Chrome, observe capture + control). A loopback integration test (Chrome → local endpoint → Playwright/MCP attach, no Tailscale) is the recommended first addition and is listed under Deferred hardening.

### D15 — Failure modes & fallbacks

- **Fail-loud + manual-retry:** detect failure, report clearly with the likely cause, exit clean (full teardown). No auto-recovery/reconnect for now (premature for supervised use; the operator re-runs).
- **Explicit handling for the modes that bite immediately:** Chrome discovery (locate the binary per-OS; clear error if absent); **CDP-port preflight** (before launching, check the `--cdp-port` isn't already held — the common case is another/your-own running Chrome, which cannot be retro-enabled for debugging (D4) — and fail loud with a clear message instead of a 20s "DevTools listening" timeout); mesh-join failure (clear error — an expired/bad key is the most common real failure); incremental capture flush (write as it streams so a mid-session drop leaves usable partial captures); and getting the `Host: localhost` rewrite exactly right.
- **"Fix if it bites" (no work now):** DERP/UDP fallback (automatic), agent-side inbound blocking (documented agent-dials-hub fallback), exotic network conditions.

---

## How the approach evolved

The record below is deliberately kept — it captures *why* the design changed, not just where it landed.

### D16 — Pivot: stop hand-writing what Tailscale + stdlib already do

An early version leaned toward hand-rolling a relay/byte-pipe. Challenged with "we're just proxying a connection Tailscale already handles — what must we actually hand-write vs. get out of the box?", research (Tailscale docs + Go stdlib) showed we'd over-built. Corrected:

- **Transport = Tailscale, not us.** `tsnet.Server.Dial(ctx, "tcp", "<agent>:<port>")` on the hub yields a raw `net.Conn` straight to the agent's Chrome; the agent exposes its port via `tsnet.Listen`. No relay, no hand-written byte-pipe.
- **The only CDP-specific logic we own** is the Chrome-94 `Host` rewrite + `webSocketDebuggerUrl` body rewrite — done with Go stdlib `net/http/httputil.ReverseProxy` (its `Rewrite` + `ModifyResponse` hooks do exactly this, and it handles WebSocket upgrades natively). Its `Transport` dials over `tsnet`.
- **Language unified on Go.** The hub became stdlib-ReverseProxy-over-`tsnet`, so it's naturally Go and embeds `tsnet` directly — superseding an earlier Deno/TS hub prototype (kept only as a reference until the Go path was proven, then removed). The local `ws://localhost:PORT` output is unchanged, so all CDP clients still attach.
- **Canonical libraries, not hand-rolled I/O:** cobra + charmbracelet/fang (CLI, styled help/errors/signals), charmbracelet/huh (consent form), lipgloss (styling) — replacing raw `flag` and a hand-written prompt.

Net effect: one language, ~one small file of real proxy logic; everything else is Tailscale + Go stdlib + well-known libraries. This is the "canonical, minimal hand-writing" north star.

### D17 / D18 — Proven: transport with built-ins, then embedded

- First proved the full path with `tailscale serve --tcp` (zero hand-rolled transport): a CDP client → hub (CDP rewrite) → Tailscale → the agent's Chrome, all working; the transport-agnostic seam meant loopback→tailnet was a one-flag change.
- Then moved to **embedded `tsnet`** (no dependency on a system Tailscale install on the user's machine — the "download → run → accept" UX), and proved it end-to-end on a real tailnet: mint an ephemeral tagged key via an OAuth client → the agent joins as an ephemeral node with no system Tailscale → the hub dials it → Playwright drives Chrome → Ctrl-C tears down Chrome + temp profile and the ephemeral node auto-deregisters. The `--serve` (system-Tailscale) path was subsequently dropped in favor of embedded-only.

### D19 — Extraction: the ephemeral link is the reusable thing; CDP is a consumer

Recognizing that what was built is really "an ephemeral secure connection between two machines, with CDP as a special case on top," the transport was extracted into **`ephlink`** — its own CDP-agnostic Go module with a **symmetric API** (`Join` → `Expose`/`Dial`/`Serve` as node capabilities; no privileged side). `agent` and `cdphub` became thin consumers; `mint` is a thin CLI over `ephlink.Mint`. `Dial` resolves a peer name → tailnet IP via online peers for robust MagicDNS. The extraction test: `ephlink`'s public API mentions nothing about CDP/Chrome/HTTP — so the same library can carry SSH, a database port, a dev server, or any TCP service.
