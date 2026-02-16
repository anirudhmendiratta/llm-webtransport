# llm-webtransport

Benchmark comparing WebTransport (QUIC) vs HTTP SSE for streaming LLM token delivery. WebTransport uses lightweight length-prefixed binary framing over QUIC streams, while HTTP SSE carries the standard `data: <token>\n\n` text format over TCP+TLS. The goal is to measure per-token overhead, TTFT, and total bytes on the wire under various network conditions.

## Prerequisites

- Go 1.25+
- OpenSSL (for certificate generation)
- [Ollama](https://ollama.com) running locally with a model pulled (default: `gemma3:12b`)

## Generate TLS Certificates

Both servers require TLS. Generate a self-signed certificate before running anything:

```bash
./generate_cert.sh
```

This creates `certs/cert.pem` and `certs/key.pem` — a P-256 ECDSA certificate valid for 365 days with `CN=localhost`.

## Project Structure

### `server/`

WebTransport server over HTTP/3 (QUIC). Listens on `:4433` and upgrades incoming requests at `/wt` to WebTransport sessions. Each client stream receives a prompt, forwards it to Ollama, and streams back tokens using a length-prefixed string protocol (`<length>:<token>`).

### `httpserver/`

HTTP SSE server over TLS. Listens on `:8080` and accepts POST requests at `/chat` with a JSON body (`{"message": "..."}`). Streams tokens back as Server-Sent Events (`data: <token>\n\n`), ending with `data: [DONE]\n\n`.

### `client/`

Interactive WebTransport client. Connects to the server on `:4433`, opens a QUIC stream, and lets you type prompts via stdin. Displays streamed tokens in real time and prints TTFT and average time-between-tokens after each response.

### `httpclient/`

Interactive HTTP SSE client. Sends prompts to the HTTP SSE server via POST and reads the SSE stream. Displays tokens in real time with the same TTFT/TBT metrics.

### `llm/`

Shared package that calls the Ollama OpenAI-compatible API (`/v1/chat/completions`) with streaming. Used by both servers.

### `message/`

Shared package implementing the length-prefixed wire protocol used by WebTransport. Format: `<length>:<payload>` (e.g. `5:hello`). Max message size is 1 MB.

### `benchmark/`

Automated benchmark harness and network-conditioned runner. See below.

## Running

Start Ollama, then launch one or both servers:

```bash
# WebTransport server (port 4433)
go run ./server

# HTTP SSE server (port 8080)
go run ./httpserver
```

Interactive clients:

```bash
go run ./client       # WebTransport
go run ./httpclient   # HTTP SSE
```

## Running Benchmarks

The benchmark compares three approaches against the same 10 prompts:

| Approach | Description |
|----------|-------------|
| **Raw API** | Direct Ollama call through a local TLS reverse proxy (baseline) |
| **HTTP SSE** | HTTP SSE server streaming `data: <token>` events over TCP+TLS |
| **WebTransport** | WebTransport server streaming length-prefixed tokens over QUIC |

### Manual run

Both servers must be running first:

```bash
# Fresh connection per prompt (measures handshake cost)
go run ./benchmark

# Reuse a single connection across all prompts
go run ./benchmark -reuse
```

### Network-conditioned benchmark

`benchmark/benchmark.sh` automates running the benchmark across multiple network profiles with packet capture. It uses macOS **dummynet** (`dnctl`) and **pf** (`pfctl`) to shape traffic on the loopback interface, and `tcpdump` to capture wire bytes per port. Requires `sudo`.

The script creates three dummynet pipes — one per server port — so each approach is shaped identically:

| Pipe | Port | Protocol | Target |
|------|------|----------|--------|
| 1 | 8080 | TCP | HTTP SSE server |
| 2 | 4433 | UDP | WebTransport server |
| 3 | 11435 | TCP | Raw API (TLS proxy to Ollama) |

It then runs the benchmark under each profile:

| Profile | dnctl params | Simulates |
|---------|-------------|-----------|
| `baseline` | *(no shaping)* | Local network |
| `latency-200ms` | `delay 200` | 200 ms one-way delay (400 ms RTT) |
| `loss-5pct` | `plr 0.05` | 5% packet loss |
| `bw-100kbps` | `bw 100Kbit/s` | 100 Kbps bandwidth cap |
| `degraded` | `delay 200 plr 0.05 bw 100Kbit/s` | All three combined |

After each profile run, the script analyzes the pcap to report total wire bytes and average bytes per packet for each approach.

```bash
# Both servers must be running, then:
./benchmark/benchmark.sh
```

Results are saved to `benchmark/results-<timestamp>.txt`.
