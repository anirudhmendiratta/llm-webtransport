#!/usr/bin/env python3
"""Parse benchmark results and compute P50 TTFT and total tokens per approach per profile."""

import re
import sys

INPUT_FILE = "/Users/anirudh/workplace/go/llm-webtransport/benchmark/results-20260216-182617.txt"

def parse_ttft(s):
    """Parse a TTFT string like '254ms' or '1.114s' into milliseconds (float)."""
    s = s.strip()
    if s.endswith("ms"):
        return float(s[:-2])
    elif s.endswith("s"):
        return float(s[:-1]) * 1000.0
    else:
        raise ValueError(f"Unknown TTFT format: {s}")

def median(values):
    """Compute median. For even-length lists, average the two middle values."""
    s = sorted(values)
    n = len(s)
    if n % 2 == 1:
        return s[n // 2]
    else:
        return (s[n // 2 - 1] + s[n // 2]) / 2.0

def format_ttft(ms):
    """Format milliseconds nicely."""
    if ms >= 1000:
        return f"{ms/1000:.3f}s"
    else:
        return f"{ms:.0f}ms"

def main():
    with open(INPUT_FILE) as f:
        text = f.read()

    # Split by profile
    profile_blocks = re.split(r"===== Profile: (\S+) =====", text)
    # profile_blocks[0] is before first profile (empty), then alternating name, content
    profiles = {}
    for i in range(1, len(profile_blocks), 2):
        name = profile_blocks[i]
        content = profile_blocks[i + 1]
        profiles[name] = content

    profile_order = ["baseline", "latency-200ms", "loss-5pct", "bw-100kbps", "degraded"]
    approach_order = ["Raw API", "HTTP SSE", "WebTransport"]

    # Pattern for each prompt line:
    # e.g. "  [1/10] What is the capital of France?... 18 tokens, TTFT 254ms, ..."
    line_pat = re.compile(
        r"\[(\d+)/10\].*?(\d+)\s+tokens,\s+TTFT\s+([\d.]+(?:ms|s))"
    )

    for profile in profile_order:
        content = profiles[profile]

        # Split into approach sections
        approach_sections = re.split(r"=== (Raw API|HTTP SSE|WebTransport) ===", content)
        # approach_sections: before first approach, then alternating name, content

        approach_data = {}
        for j in range(1, len(approach_sections), 2):
            aname = approach_sections[j]
            asection = approach_sections[j + 1]

            ttfts = []
            tokens = []
            for m in line_pat.finditer(asection):
                tok = int(m.group(2))
                ttft_str = m.group(3)
                ttft_ms = parse_ttft(ttft_str)
                tokens.append(tok)
                ttfts.append(ttft_ms)

            approach_data[aname] = {
                "ttfts": ttfts,
                "tokens": tokens,
                "p50_ttft": median(ttfts),
                "total_tokens": sum(tokens),
            }

        # Print table
        print(f"{'=' * 60}")
        print(f"  Profile: {profile}")
        print(f"{'=' * 60}")
        print(f"  {'Approach':<16} {'P50 TTFT':>12} {'Total Tokens':>14}")
        print(f"  {'-'*16} {'-'*12} {'-'*14}")
        for a in approach_order:
            d = approach_data[a]
            p50 = format_ttft(d["p50_ttft"])
            total_tok = d["total_tokens"]
            print(f"  {a:<16} {p50:>12} {total_tok:>14}")

        # Show sorted TTFT values for verification
        print()
        print(f"  Per-prompt TTFT values (sorted):")
        for a in approach_order:
            d = approach_data[a]
            sorted_ttfts = sorted(d["ttfts"])
            vals = ", ".join(format_ttft(v) for v in sorted_ttfts)
            print(f"    {a:<16}: {vals}")
            print(f"    {'':16}  -> P50 = avg of [{format_ttft(sorted_ttfts[4])}, {format_ttft(sorted_ttfts[5])}] = {format_ttft(d['p50_ttft'])}")
        print()

if __name__ == "__main__":
    main()
