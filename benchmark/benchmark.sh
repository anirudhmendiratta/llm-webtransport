#!/usr/bin/env bash
set -euo pipefail

# Network-conditioned benchmark runner (connection reuse mode)
# Requires sudo for dnctl/pfctl (macOS dummynet) and tcpdump
#
# Pipes:
#   pipe 1 — TCP port 8080  (HTTP SSE server)
#   pipe 2 — UDP port 4433  (WebTransport server)
#   pipe 3 — TCP port 11435 (Raw API → TLS proxy to Ollama)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RESULTS_FILE="$SCRIPT_DIR/results-${TIMESTAMP}.txt"
PF_RULES_FILE="$(mktemp /tmp/benchmark-pf.XXXXXX)"
PCAP_DIR="$(mktemp -d /tmp/benchmark-pcap.XXXXXX)"

PROFILES="baseline latency-200ms loss-5pct bw-100kbps degraded"

# Map profile name to dnctl params
profile_params() {
  case "$1" in
    baseline)      echo "" ;;
    latency-200ms) echo "delay 200" ;;
    loss-5pct)     echo "plr 0.05" ;;
    bw-100kbps)    echo "bw 100Kbit/s" ;;
    degraded)      echo "delay 200 plr 0.05 bw 100Kbit/s" ;;
    *)             echo ""; echo "Unknown profile: $1" >&2; exit 1 ;;
  esac
}

# --- Cleanup function ---
cleanup() {
  echo ""
  echo "Cleaning up..."
  # Stop any lingering tcpdump
  sudo kill "$TCPDUMP_PID" 2>/dev/null || true
  sudo pfctl -d 2>/dev/null || true
  sudo dnctl flush 2>/dev/null || true
  rm -f "$PF_RULES_FILE"
  rm -rf "$PCAP_DIR"
  echo "Cleanup done."
}
TCPDUMP_PID=""
trap cleanup EXIT

# --- Helper: apply shaping for a profile ---
apply_shaping() {
  local params="$1"

  sudo dnctl pipe 1 config $params   # TCP :8080
  sudo dnctl pipe 2 config $params   # UDP :4433
  sudo dnctl pipe 3 config $params   # TCP :11435

  cat > "$PF_RULES_FILE" <<EOF
dummynet out proto tcp from any to localhost port 8080 pipe 1
dummynet out proto udp from any to localhost port 4433 pipe 2
dummynet out proto tcp from any to localhost port 11435 pipe 3
EOF

  sudo pfctl -f "$PF_RULES_FILE" 2>/dev/null
  sudo pfctl -e 2>/dev/null || true
}

remove_shaping() {
  sudo pfctl -d 2>/dev/null || true
  sudo dnctl flush 2>/dev/null || true
}

# --- Helper: analyze pcap wire bytes per port ---
analyze_wire_bytes() {
  local pcap_file="$1"
  local label

  echo ""
  echo "Wire bytes (total on-the-wire including all protocol headers):"

  for port_info in "11435:Raw API" "8080:HTTP SSE" "4433:WebTransport"; do
    local port="${port_info%%:*}"
    label="${port_info##*:}"
    local filtered="$PCAP_DIR/filtered-${port}.pcap"

    # Filter pcap by port into a separate file
    sudo tcpdump -r "$pcap_file" "port $port" -w "$filtered" 2>/dev/null

    if [ ! -f "$filtered" ]; then
      echo "  $label (port $port): no packets captured"
      continue
    fi

    # Count packets (tcpdump -nn to stdout, one line per packet)
    local packets
    packets=$(sudo tcpdump -r "$filtered" -nn -q 2>/dev/null | wc -l | tr -d ' ')

    if [ "$packets" -eq 0 ] 2>/dev/null; then
      echo "  $label (port $port): no packets captured"
      rm -f "$filtered"
      continue
    fi

    # Wire bytes = pcap file size - global header (24B) - per-packet headers (16B each)
    local filesize
    filesize=$(stat -f%z "$filtered")
    local wire_bytes=$((filesize - 24 - packets * 16))

    echo "  $label (port $port): $wire_bytes bytes over $packets packets ($(( wire_bytes / packets )) avg bytes/pkt)"
    rm -f "$filtered"
  done
}

# --- Main ---

# Prompt for sudo once upfront and keep credentials cached.
sudo -v
while true; do sudo -n true; sleep 50; kill -0 "$$" || exit; done 2>/dev/null &

echo "=== Network-Conditioned Benchmark (Connection Reuse) ==="
echo "Results will be saved to: $RESULTS_FILE"
echo ""

for profile in $PROFILES; do
  params="$(profile_params "$profile")"
  header="===== Profile: $profile ====="
  if [ -n "$params" ]; then
    header="$header  (dnctl: $params)"
  fi

  echo ""
  echo "$header"
  echo "$header" >> "$RESULTS_FILE"

  if [ "$profile" = "baseline" ]; then
    remove_shaping
  else
    apply_shaping "$params"
  fi

  # Start packet capture
  PCAP_FILE="$PCAP_DIR/${profile}.pcap"
  sudo tcpdump -i lo0 -w "$PCAP_FILE" \
    '(port 8080 or port 4433 or port 11435)' 2>/dev/null &
  TCPDUMP_PID=$!
  sleep 1  # let tcpdump initialize

  # Run the Go benchmark with connection reuse
  (cd "$PROJECT_DIR" && go run ./benchmark/ -reuse) 2>&1 | tee -a "$RESULTS_FILE"

  # Stop tcpdump
  sudo kill "$TCPDUMP_PID" 2>/dev/null || true
  wait "$TCPDUMP_PID" 2>/dev/null || true
  TCPDUMP_PID=""
  sleep 0.5  # let pcap flush

  # Analyze wire bytes from pcap
  analyze_wire_bytes "$PCAP_FILE" 2>&1 | tee -a "$RESULTS_FILE"
  rm -f "$PCAP_FILE"

  # Remove shaping between profiles
  if [ "$profile" != "baseline" ]; then
    remove_shaping
  fi

  echo "" >> "$RESULTS_FILE"
done

echo ""
echo "All profiles complete. Results saved to: $RESULTS_FILE"
