# Benchmark Results: Connection Reuse Mode (Incorrect results where HTTP was not reusing connection properly)

**Date:** 2026-02-16
**Model:** gemma3:12b (via Ollama)
**Prompts:** 10 diverse prompts per run

**Setup:**
- **Connection reuse enabled** — simulates a persistent browser connection (no handshake per prompt)
- Raw API: persistent `http.Client` with keep-alives, through TLS reverse proxy (port 11435)
- HTTP SSE: persistent `http.Client` with keep-alives, TLS on port 8080
- WebTransport: single QUIC+TLS session reused, new stream per prompt (port 4433)
- All servers use TLS with the same self-signed certificate
- Ollama (port 11434) is unshaped — only the client-facing leg is degraded
- Network shaping via macOS `dnctl`/`pfctl` dummynet
- Wire bytes measured via `tcpdump` on loopback (includes IP/TCP/UDP/QUIC headers)
- Ollama warmed up before each profile to avoid cold-start skew

**TLS verification:** All three approaches use TLS. The WebTransport server (`server/main.go`) loads `certs/cert.pem` and serves HTTP/3 over QUIC+TLS. The HTTP SSE server (`httpserver/main.go`) uses `ListenAndServeTLS`. The Raw API goes through a TLS reverse proxy. The benchmark client uses `InsecureSkipVerify: true` for all three since the cert is self-signed.

## Summary Tables

*Avg Bytes and Avg B/tok are wire-level measurements from tcpdump, including IP, TCP/UDP, TLS/QUIC, and application headers.*

### Baseline (no network shaping)

| Approach | Avg Bytes | Avg TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 187,005 | 265ms | 35ms | 17.5s | 494 | 378.5 |
| HTTP SSE | 107,157 | 269ms | 35ms | 18.8s | 532 | 201.4 |
| WebTransport | 60,864 | 263ms | 35ms | 16.2s | 459 | 132.6 |

### Latency 200ms (400ms RTT)

| Approach | Avg Bytes | Avg TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 186,454 | 914ms | 35ms | 18.0s | 490 | 380.5 |
| HTTP SSE | 97,931 | 916ms | 35ms | 17.5s | 477 | 205.3 |
| WebTransport | 66,295 | **506ms** | 35ms | 17.8s | 497 | 133.4 |

### Packet Loss 5%

| Approach | Avg Bytes | Avg TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 183,524 | 272ms | 35ms | 17.3s | 488 | 376.1 |
| HTTP SSE | 88,309 | 294ms | 35ms | 15.7s | 442 | 199.8 |
| WebTransport | 67,539 | 268ms | 35ms | 17.0s | 482 | 140.1 |

### Bandwidth 100 Kbit/s

| Approach | Avg Bytes | Avg TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 183,477 | 628ms | 35ms | 17.1s | 472 | 388.7 |
| HTTP SSE | 100,011 | 644ms | 35ms | 17.1s | 471 | 212.3 |
| WebTransport | 63,334 | **274ms** | 35ms | 16.9s | 479 | 132.2 |

### Degraded (200ms delay + 5% loss + 100 Kbit/s)

| Approach | Avg Bytes | Avg TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 180,698 | 1,154ms | 35ms | 17.7s | 475 | 380.4 |
| HTTP SSE | 95,740 | 1,225ms | 35ms | 17.8s | 475 | 201.6 |
| WebTransport | 61,552 | **521ms** | 35ms | 16.8s | 468 | 131.5 |

## Wire Bytes (on-the-wire including protocol headers)

| Profile | Raw API | HTTP SSE | WebTransport |
|---|---|---|---|
| Baseline | 1,869,948 (10,092 pkts, 379 B/tok) | 1,071,571 (10,845 pkts, 201 B/tok) | 608,635 (9,265 pkts, 133 B/tok) |
| Latency 200ms | 1,864,542 (10,084 pkts, 381 B/tok) | 979,313 (9,818 pkts, 205 B/tok) | 662,946 (10,047 pkts, 133 B/tok) |
| Loss 5% | 1,835,241 (9,740 pkts, 376 B/tok) | 883,087 (8,822 pkts, 200 B/tok) | 675,390 (10,304 pkts, 140 B/tok) |
| BW 100kbps | 1,834,769 (9,741 pkts, 389 B/tok) | 1,000,110 (9,728 pkts, 212 B/tok) | 633,343 (9,657 pkts, 132 B/tok) |
| Degraded | 1,806,982 (9,567 pkts, 380 B/tok) | 957,400 (9,537 pkts, 202 B/tok) | 615,515 (9,340 pkts, 132 B/tok) |

Wire byte ratios (baseline):
- Raw API : HTTP SSE : WebTransport = **3.1x : 1.8x : 1x**

## Analysis

### TTFT: WebTransport advantage at bandwidth constraints

The most significant finding is WebTransport's TTFT advantage under bandwidth-constrained conditions:

| Profile | Raw API | HTTP SSE | WebTransport | WT advantage |
|---|---|---|---|---|
| Baseline | 265ms | 269ms | 263ms | — |
| Latency 200ms | 914ms | 916ms | **506ms** | **~45% lower** |
| Loss 5% | 272ms | 294ms | 268ms | — |
| BW 100kbps | 628ms | 644ms | **274ms** | **~57% lower** |
| Degraded | 1,154ms | 1,225ms | **521ms** | **~57% lower** |

Under the 100 Kbit/s bandwidth cap, WebTransport TTFT (274ms) is nearly identical to baseline, while Raw API (628ms) and HTTP SSE (644ms) are more than 2x slower.

**Why this happens:** With connection reuse, no handshake is needed for any approach. But each new request still has protocol-level overhead that must traverse the constrained link before Ollama starts generating tokens:

- **Raw API (TCP+TLS):** The HTTP request includes headers (Content-Type, Host, etc.) plus the full JSON body (`{"model":"gemma3:12b","messages":[...],"stream":true}`), and the response includes HTTP headers + chunked transfer encoding + JSON SSE framing. At 100 Kbit/s, this larger payload takes measurably longer to transmit.
- **HTTP SSE (TCP+TLS):** Similar HTTP overhead, though smaller JSON payload.
- **WebTransport (QUIC):** Opens a new stream on the existing session (essentially free — no round trip), then sends only the compact length-prefixed prompt bytes. The response is also compact binary framing. Far less data needs to squeeze through the constrained link.

**Note on 1 Mbit/s bandwidth:** In the [previous benchmark](RESULTS.md) with fresh connections at 1 Mbit/s, all three approaches showed identical TTFT (~293-306ms). The bandwidth cap only becomes a differentiator at much lower throughput (100 Kbit/s) where protocol overhead is a significant fraction of the available bandwidth.

### TTFT: WebTransport advantage under latency with connection reuse

At 200ms delay, WebTransport shows 506ms TTFT vs ~915ms for both TCP-based approaches. This is a striking result specific to connection reuse mode.

With a persistent QUIC session, opening a new stream is instantaneous (no round trip). But even with HTTP keep-alive, each new HTTP request-response cycle over TCP may incur additional round trips for the request/response exchange before data begins streaming. The 200ms delay amplifies this difference.

### Wire Efficiency

Wire-level bytes per token (measured via tcpdump, including IP/TCP/UDP/TLS/QUIC headers):

| Approach | Wire B/tok | vs Raw API |
|---|---|---|
| Raw API | ~379 B/tok | — (baseline) |
| HTTP SSE | ~201 B/tok | **47% less** |
| WebTransport | ~133 B/tok | **65% less** |

At the application level, the ratios are more extreme (228 : 12 : 6 B/tok) because protocol headers are not counted. At the wire level, the fixed per-packet overhead (IP + TCP/UDP + TLS/QUIC headers) narrows the gap since all approaches send a similar number of packets (~10,000). But WebTransport still uses **65% less bandwidth per token** than Raw API and **34% less** than HTTP SSE.

Average wire bytes per token:
- Raw API: **~379 B/tok** — JSON SSE framing + HTTP/TLS overhead
- HTTP SSE: **~201 B/tok** — SSE `data:` framing + HTTP/TLS overhead
- WebTransport: **~133 B/tok** — compact binary framing + QUIC overhead

### Inter-Token Latency and Total Time

TBT remains **35ms across all profiles and approaches**, confirming it is entirely LLM-bound (~29 tokens/sec). Total time also stays in the 16-18s range, driven by response length variation rather than protocol differences.

### Packet Loss

At 5% loss, all three approaches show minimal TTFT degradation. The low throughput (~29 tok/s) means packet loss events are rare and both TCP retransmission and QUIC loss recovery handle them within the inter-token gap.

## Key Takeaways

1. **WebTransport has a real TTFT advantage under bandwidth constraints.** At 100 Kbit/s, WebTransport TTFT is 57% lower than HTTP SSE (274ms vs 644ms). Its compact framing means less data must traverse the constrained link before tokens start flowing. At 1 Mbit/s (previous benchmark), there was no measurable difference — the advantage only emerges when bandwidth is severely constrained.

2. **Connection reuse unlocks WebTransport's latency advantage.** At 200ms delay with persistent connections, WebTransport TTFT is ~45% lower (506ms vs 916ms). Opening a new QUIC stream is free (no round trip), while HTTP request/response exchange still incurs latency over TCP.

3. **WebTransport uses 65% less wire bandwidth per token than Raw API** (~133 vs ~379 B/tok) and 34% less than HTTP SSE (~201 B/tok), measured at the packet level including all protocol headers.

4. **Wire efficiency is consistent across conditions.** WebTransport averages ~133 B/tok vs ~201 B/tok (HTTP SSE) vs ~379 B/tok (Raw API), regardless of network profile.

5. **TBT is always LLM-bound.** No protocol or network condition affects inter-token latency — the model's generation speed (~35ms/token) is the sole bottleneck for streaming delivery.

6. **Packet loss at 5% has negligible impact** on any approach at this throughput level.
