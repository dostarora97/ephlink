# Vision & north star

> Status: **north-star ideation.** This is the exhaustive articulation of where the project is
> headed — deliberately ambitious and complete, not a commitment to build order. Individual
> capabilities are tracked as GitHub issues (see the epic linked at the bottom). What exists today
> is a small first slice of Layer 2 over a working Layer 1.

## The two layers

### Layer 1 — `ephlink`: a universal drop-in transport

**Drop the host binary into anything, and the client can wire arbitrary things to-and-fro over an ephemeral, secure, peer-to-peer mesh.** Today the cargo is CDP; tomorrow it is anything — SSH, a database port, a file share, a gRPC service, a dev server, a game server, another mesh service.

- The transport is the durable product; consumers are pluggable.
- Embedded Tailscale (`tsnet`), WireGuard end-to-end, **no public surface**, ephemeral identity that auto-deregisters. (The research across the remote-browser field found no other tool exposing its endpoint over embedded-tsnet — this is the moat.)
- `ephlink` never learns about CDP or any specific protocol. It moves bytes between a named node and a local service, with an optional per-connection `Transform`. Everything protocol-specific lives in a consumer layer above it.

### Layer 2 — total browser observability + control

**For the browser specifically: a complete instrumentation and control fabric for a live remote browser** — capture *everything*, control *everything*, transform *anything*, for three kinds of driver, both passively and actively.

- **Three drivers:** [1] humans, [2] agents (LLMs), [3] scripts — able to observe and/or drive the **same** live session, ideally simultaneously.
- **Two modes throughout:** **passive** (observe / record / log) and **active** (drive / inject / block / rewrite) — for every surface below.
- Not "drive a browser" (commodity). A **programmable, recordable, MITM-able digital twin of a live browser.**

## The complete instrumentation surface

Everything below should be capturable (passive) and, where meaningful, controllable/transformable (active). Grouped by how we get at it.

### A. Protocol-native (out-of-the-box from CDP, and its cross-browser successor BiDi)

- **Network** — every request/response, bodies, headers, timing, WebSocket/EventSource frames (full HAR). **Active MITM:** block, modify, mock, redirect, inject, delay before it reaches the page (`Fetch`/`Network` interception).
- **Console & errors** — console messages, `Runtime.exceptionThrown`, unhandled rejections, log streams.
- **DOM** — full tree snapshots + live mutation events; query, modify, watch.
- **Input** — synthesize and observe mouse / keyboard / touch / wheel.
- **Page lifecycle** — navigations, frame tree, dialogs, downloads, file choosers, screencast frames.
- **JavaScript** — `Runtime.evaluate` (arbitrary JS), `addScriptToEvaluateOnNewDocument` (**pre-page** injection — instrument before any site code runs), `Debugger.*` breakpoints/stepping, `Profiler.*` CPU/heap.
- **Storage** — cookies, localStorage, sessionStorage, IndexedDB, CacheStorage, service workers.
- **Emulation** — device, viewport, geolocation, timezone, locale, network throttling, sensors, CPU throttling.
- **Performance & tracing** — `Performance.*` metrics, `Tracing.*`, JS/CSS coverage, Lighthouse-grade audits.
- **Accessibility** — the a11y tree (the structured view LLM/MCP tools drive from).
- **Security** — certs, mixed content, CSP violations.
- **Media / screencast** — `Page.startScreencast` frames; a video track for humans.

### B. Injected / userland instrumentation (beyond what the protocol exposes)

Injected pre-page so it wraps site code from frame zero.

- **rrweb** — full DOM + mutation stream as a compact, scrubbable **semantic timeline** (records DOM deltas via MutationObserver, replays by rebuilding the DOM without executing JS). The "what did the page actually do" record; tiny vs. video, pixel-faithful, DOM-inspectable at any point. (Canvas / media need opt-in plugins.)
- **Web-API proxying / trapping** — inject a shim that wraps or `Proxy`-traps every Web API to log *and optionally intercept* every invocation & mutation: `fetch` / `XHR`, `WebSocket`, `Notification`, `Geolocation`, `Clipboard`, `WebRTC`, `Canvas` / `WebGL`, `postMessage`, `history`, timers, `MutationObserver` on everything.
- **Storage mutation streams** — live streams of every localStorage / sessionStorage / IndexedDB / cookie mutation (not just snapshots).
- **Interaction streams** — clicks, scrolls, focus, hover, key timing, idle/timeouts, visibility changes, form input, drag — the full behavioral trace.
- **Continuous rendered-DOM capture** — periodic/triggered snapshots of the computed, rendered state.

### C. Higher-order capabilities (composed from A + B)

- **Unified session-replay bundle** — A + B fused into one **time-aligned timeline**: correlate *this network call ↔ this DOM mutation ↔ this click ↔ this console error*, scrub, export.
- **JS source & sourcemap capture** — every script the page loaded, resolved through sourcemaps, for real debugging.
- **Multi-track capture** — pixels (screencast/WebRTC) **+** DOM (rrweb) **+** network (HAR) **+** behavior (interaction stream), all time-aligned; pick the right lens per consumer.
- **Deterministic record-and-replay** — capture enough (network + inputs + timing + clock/PRNG seeds) to **re-run** a session offline, deterministically. The holy grail for debugging/testing; rarely done well.
- **Redaction / policy layer** — since captures carry live tokens/passwords, a transform stage that scrubs/masks sensitive fields on the way out. (Enforces the D9 capture riders.)
- **Live tap / fan-out** — multiple consumers on the **same** session at once (human mirror + LLM endpoint + a recorder + a MITM), enabled by the one-stream-many-consumers architecture.
- **Human ↔ agent co-driving** — LLM drives via the protocol; a human watches the mirror and takes the wheel when needed, then hands back. No surveyed product does this; the shared-bus design makes it natural.

## Architecture implication: a composable instrumentation pipeline

Today's `client` does one CDP rewrite and pipes bytes. The north star needs the browser's entire I/O to be treated as an **event bus you can subscribe to and rewrite** — a stack of composable **taps/transforms** on the CDP/BiDi stream (plus the injected userland hooks), where each capability is a pluggable **stage** that can observe and/or mutate the flow:

```
Chrome/CDP ─▶ [tap: HAR] ─▶ [transform: network MITM] ─▶ [tap: rrweb inject] ─▶ [tap: interaction] ─▶
             [transform: CDP Host rewrite] ─▶ [transform: redact] ─▶ fan-out ─▶ { human mirror, LLM/MCP, recorder, script }
```

This splits cleanly along the two layers:
- **`ephlink`** stays dumb transport — never grows protocol knowledge.
- A new **browser-instrumentation layer** (working name TBD) is the composable pipeline. CDP- and BiDi-aware. The human mirror, the LLM/MCP endpoint, the recorders, and the MITM are all *stages* in it — not separate one-off proxies.

## Protocol backends

- **CDP** — today's primary; what the LLM/MCP ecosystem uses.
- **WebDriver BiDi** — the W3C cross-browser bidirectional protocol (Chrome/Firefox/WebKit converging). The hedge against Chrome-lock; a second backend beside CDP. (#28)

## Prior art we borrow from (not reinvent)

- **Kernel** (onkernel/kernel-images) — closest twin: CDP-on-:9222 for agents **+** interactive live view (noVNC + a Neko-derived WebRTC client). Difference: it exposes via public ports/TURN; we use a private mesh. Borrow its WebRTC live-view client for the human mirror.
- **Neko** (m1k1o/neko) — gold-standard human remote-browser: GStreamer captures X11 → VP8/H.264 → WebRTC media + input over a data channel, with multi-user control handoff. Borrow the WebRTC path and the control-handoff model.
- **rrweb** — the DOM-mirror capture/replay engine for Layer 2/B.
- **Playwright MCP / chrome-devtools MCP / browser-use** — the agent consumers; attach to our CDP/ BiDi endpoint unchanged.
- **Surfly / Menlo (DOM-mirror)** — the proxy co-browse / DOM-stream lineage, if we want pixel-free human view.

## What's genuinely novel here

The individual primitives are all solved. The novel composition is: **a total, composable, record-everything / rewrite-anything browser-instrumentation fabric — drivable by human + agent + script simultaneously — carried over a private, zero-public-surface, ephemeral mesh, on a transport generic enough to carry anything else too.** No surveyed product combines the private-mesh transport, the full instrumentation surface, and the simultaneous human/agent/script control.

## Cross-cutting concerns not yet addressed

The capability list above (the instrumentation surface) describes *what* we capture and control. It does **not** yet answer the platform-level, cross-cutting questions below. These are architectural dimensions, not features — most capabilities depend on them, and leaving them implicit is how a capable prototype fails to become a platform. Enumerated here so the north star is honest about its own gaps.

- **Persistence & session state.** Everything today is *live*. There is no story for saving a session and resuming it, snapshotting browser state (auth/cookies/profile — cf. Kernel's unikernel snapshot/standby), or reusing a real profile (`--real-profile` is still unbuilt). "Record everything" implies a storage + lifecycle subsystem the capability list never names.

- **Storage, indexing & retrieval of captures.** Capture is only half; the artifacts (HAR, rrweb streams, video, interaction logs) must be stored, indexed, addressed, retained/purged, and encrypted at rest (ties to the D9 riders, #8). Where do captures live, in what format, keyed how? A write-only firehose is not the goal.

- **A query / analysis layer over captures.** The value of "record everything" is *asking questions* of it — "every failed request across these sessions", "diff this run vs. that run", "when did this DOM node change". Capture without query is a write-only log. Entirely absent from the vision so far.

- **Identity, authorization & multi-tenancy.** Access today is "possess an ephemeral key". There is no notion of *identity-scoped* access — who may attach to / observe / drive whose browser, at what privilege (observe vs. drive vs. MITM). Mandatory for anything beyond you-driving-your-own-machines. (tsidp / OIDC #20 is the seed.)

- **The consumer contract (API / SDK / event schema).** The host side is specified exhaustively; the *consumer* side is not. How does a script or app subscribe to the instrumentation streams and issue control? A stable event schema, a subscription API, an SDK — this is arguably the most important missing piece for being a *platform* rather than a demo.

- **Scale: multi-host × multi-session × multi-consumer.** The vision is single-session-centric. Real use is N hosts × M sessions × K consumers, needing orchestration, session discovery/lifecycle, and a control plane (fleet-MCP #22 gestures at this).

- **Self-observability & backpressure.** A record-everything system that silently drops events under load is worse than none. The tool needs its own health/metrics, and defined backpressure semantics when a consumer or the mesh can't keep up.

- **Failure semantics under partial capture.** When the mesh blips mid-session, what happens to in-flight capture/replay/drive? The pipeline design must define partial-failure behavior, not assume a clean pipe.

- **A second, concrete non-browser cargo.** Layer 1 promises "carry anything," but until a *second* real consumer exists (SSH, a DB inspector, a dev server), "generic" is aspirational. One concrete example proves the transport claim. (#17 is the seed.)

## Honest status (2026-07)

- **Layer 1 (transport): ~85% built.** Join / Dial / Expose / ServeOnMesh / ListenOnMesh / Mint, ephemeral nodes, fleet management, proven Mac↔Windows. This is the hard, novel, defensible part — and it's essentially done. Remaining: genericity polish (#9, #16, #17), reaping bug (#10).
- **Layer 2 (browser fabric): ~10% built.** Today = raw CDP passthrough + the Host/webSocketDebuggerUrl rewrite (one small first slice). The capture / MITM / replay / mirror / multi-consumer fabric is design + issues only.
- **Effort caveat:** by feature-count, much of Layer 2 is glue over solved libraries (HAR, rrweb, screencast, MCP). By person-months, the effort is dominated by the parts that are *not* glue: the composable pipeline (#30, the linchpin), multi-consumer + human↔agent co-driving (#40), a good WebRTC mirror (#27, not a drop-in from Neko/Kernel because we have no X11 desktop), active network MITM (#33), and deterministic replay (#38).

---

Tracked as a GitHub epic with one sub-issue per capability group. See the issues labeled `vision`.

