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

---

Tracked as a GitHub epic with one sub-issue per capability group. See the issues labeled `vision`.
