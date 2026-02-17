# Benchmark Results: Connection Reuse Mode (Fixed)

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

**What changed from [RESULTS-2](RESULTS-2.md):** The previous benchmark had a bug where the HTTP SSE and Raw API clients broke out of the response scanner loop on `data: [DONE]` without draining the remaining response body to EOF. This prevented Go's `http.Transport` from returning connections to the keep-alive pool, causing a fresh TCP+TLS handshake on every prompt despite the `-reuse` flag. The fix adds `io.Copy(io.Discard, resp.Body)` after the scanner loop so the transport sees EOF and correctly pools the connection.

## Summary Tables

*Avg Bytes and Avg B/tok are wire-level measurements from tcpdump, including IP, TCP/UDP, TLS/QUIC, and application headers. P50 TTFT is the median across 10 prompts to exclude first-prompt handshake outliers.*

### Baseline (no network shaping)

| Approach | Avg Bytes | P50 TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 163,356 | 270ms | 35ms | 15.5s | 438 | 372.7 |
| HTTP SSE | 94,345 | 262ms | 35ms | 17.2s | 485 | 194.5 |
| WebTransport | 66,350 | 262ms | 35ms | 17.7s | 502 | 132.0 |

### Latency 200ms

| Approach | Avg Bytes | P50 TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 172,422 | 508ms | 35ms | 16.6s | 461 | 373.4 |
| HTTP SSE | 104,102 | 508ms | 35ms | 19.3s | 536 | 194.1 |
| WebTransport | 66,535 | 510ms | 35ms | 17.9s | 500 | 132.8 |

### Packet Loss 5%

| Approach | Avg Bytes | P50 TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 177,208 | 262ms | 35ms | 17.0s | 479 | 369.4 |
| HTTP SSE | 82,300 | 262ms | 35ms | 15.2s | 430 | 191.1 |
| WebTransport | 62,596 | 263ms | 35ms | 15.8s | 448 | 139.7 |

### Bandwidth 100 Kbit/s

| Approach | Avg Bytes | P50 TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 190,878 | 296ms | 35ms | 18.2s | 512 | 372.4 |
| HTTP SSE | 96,018 | 299ms | 35ms | 17.5s | 494 | 194.2 |
| WebTransport | 63,739 | **276ms** | 35ms | 17.0s | 482 | 132.0 |

### Degraded (200ms delay + 5% loss + 100 Kbit/s)

| Approach | Avg Bytes | P50 TTFT | Avg TBT | Avg Total | Avg Tokens | Avg B/tok |
|---|---|---|---|---|---|---|
| Raw API | 177,472 | 545ms | 35ms | 17.3s | 479 | 369.8 |
| HTTP SSE | 96,372 | 539ms | 35ms | 18.2s | 505 | 190.8 |
| WebTransport | 62,132 | **517ms** | 35ms | 16.8s | 470 | 132.2 |

## Wire Bytes (on-the-wire including protocol headers)

| Profile | Raw API | HTTP SSE | WebTransport |
|---|---|---|---|
| Baseline | 1,633,564 (8,934 pkts, 373 B/tok) | 943,450 (9,811 pkts, 195 B/tok) | 663,502 (10,133 pkts, 132 B/tok) |
| Latency 200ms | 1,724,222 (9,422 pkts, 373 B/tok) | 1,041,018 (10,842 pkts, 194 B/tok) | 665,346 (10,085 pkts, 133 B/tok) |
| Loss 5% | 1,772,083 (9,513 pkts, 369 B/tok) | 822,998 (8,512 pkts, 191 B/tok) | 625,964 (9,529 pkts, 140 B/tok) |
| BW 100kbps | 1,908,780 (10,411 pkts, 372 B/tok) | 960,183 (9,996 pkts, 194 B/tok) | 637,390 (9,717 pkts, 132 B/tok) |
| Degraded | 1,774,723 (9,528 pkts, 370 B/tok) | 963,715 (9,966 pkts, 191 B/tok) | 621,315 (9,422 pkts, 132 B/tok) |

Wire byte ratios (baseline):
- Raw API : HTTP SSE : WebTransport = **2.5x : 1.4x : 1x**

## Analysis

### The connection reuse fix eliminates the TTFT gap under latency

The most important finding: the ~400ms TTFT advantage WebTransport showed over HTTP SSE in [RESULTS-2](RESULTS-2.md) was **entirely caused by broken HTTP connection reuse**, not an inherent protocol advantage.

| Profile | Approach | RESULTS-2 (broken reuse) | RESULTS-3 (fixed reuse) | Change |
|---|---|---|---|---|
| Latency 200ms | Raw API | 914ms avg | **508ms** P50 | **-406ms** |
| | HTTP SSE | 916ms avg | **508ms** P50 | **-408ms** |
| | WebTransport | 506ms avg | **510ms** P50 | +4ms (unchanged) |
| Degraded | Raw API | 1,154ms avg | **545ms** P50 | **-609ms** |
| | HTTP SSE | 1,225ms avg | **539ms** P50 | **-686ms** |
| | WebTransport | 521ms avg | **517ms** P50 | -4ms (unchanged) |

With working keep-alive, all three approaches now have **effectively identical P50 TTFT** under the 200ms latency profile (~508-510ms). The previous 400ms gap came from HTTP re-establishing TCP+TLS on every prompt (3 client-to-server packets delayed 200ms each = 600ms overhead) while WebTransport's QUIC session was properly reused all along.

### WebTransport retains a small TTFT edge under bandwidth constraints

Under the 100 Kbit/s bandwidth cap, WebTransport still shows a measurable TTFT advantage:

| Profile | Raw API | HTTP SSE | WebTransport | WT advantage |
|---|---|---|---|---|
| Baseline | 270ms | 262ms | 262ms | — |
| BW 100kbps | 296ms | 299ms | **276ms** | **~8% lower** |
| Degraded | 545ms | 539ms | **517ms** | **~4% lower** |

The gap is much smaller than RESULTS-2 reported (~57% lower) because connection reuse is now working. The remaining ~23ms advantage comes from WebTransport's smaller per-request payload: opening a QUIC stream and sending a length-prefixed prompt (~50 bytes) squeezes through the bandwidth-constrained link faster than HTTP headers + JSON body (~200-300 bytes).

### Wire Efficiency

Wire-level bytes per token (measured via tcpdump, including IP/TCP/UDP/TLS/QUIC headers):

| Approach | Wire B/tok | vs Raw API |
|---|---|---|
| Raw API | ~372 B/tok | — (baseline) |
| HTTP SSE | ~194 B/tok | **48% less** |
| WebTransport | ~132 B/tok | **64% less** |

At the application level, the ratios are more extreme (229 : 12 : 6 B/tok) because protocol headers are not counted. At the wire level, the fixed per-packet overhead (IP + TCP/UDP + TLS/QUIC headers) narrows the gap since all approaches send a similar number of packets (~9,000-10,000). But WebTransport still uses **64% less bandwidth per token** than Raw API and **32% less** than HTTP SSE.

### Inter-Token Latency and Total Time

TBT remains **35ms across all profiles and approaches**, confirming it is entirely LLM-bound (~29 tokens/sec). Total time stays in the 15-19s range, driven by response length variation rather than protocol differences.

### Packet Loss

At 5% loss, all three approaches show minimal TTFT degradation (P50 within 1ms of baseline). The low throughput (~29 tok/s) means packet loss events are rare and both TCP retransmission and QUIC loss recovery handle them within the inter-token gap.

## Key Takeaways

1. **The TTFT advantage from RESULTS-2 was a connection reuse bug, not a protocol advantage.** The previous ~400ms gap under 200ms latency was caused by the HTTP client failing to drain the response body to EOF, which prevented Go's `http.Transport` from pooling connections. With the fix, all three approaches show identical P50 TTFT (~508-510ms) under latency.

2. **WebTransport still uses 64% less wire bandwidth per token than Raw API** (~132 vs ~372 B/tok) and 32% less than HTTP SSE (~194 B/tok), measured at the packet level including all protocol headers.

3. **Under bandwidth constraints, WebTransport has a small but real TTFT edge** (~23ms at 100 Kbit/s). Its compact binary framing means less data must traverse the constrained link before tokens start flowing. This advantage is modest with proper connection reuse but would compound in scenarios with many concurrent requests sharing limited bandwidth.

4. **QUIC session reuse is more robust than HTTP keep-alive.** The broken-reuse bug that affected HTTP (failure to drain response body before `Close()`) cannot happen with WebTransport's stream-per-request model, where each stream reaches a clean EOF when the server closes it. HTTP keep-alive requires careful response body lifecycle management that is easy to get wrong.

5. **TBT is always LLM-bound.** No protocol or network condition affects inter-token latency — the model's generation speed (~35ms/token) is the sole bottleneck for streaming delivery.

6. **No cold-start handshake advantage.** A [previous benchmark](RESULTS.md) with fresh connections per prompt under 200ms latency showed identical TTFT across all three approaches (~916ms). QUIC's combined transport+crypto handshake (1 RTT) is offset by the HTTP/3 CONNECT upgrade needed for WebTransport, giving it no net advantage over TCP+TLS 1.3 (2 RTT) for initial connection setup.

7. **Packet loss at 5% has negligible impact** on any approach at this throughput level.
