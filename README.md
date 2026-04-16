# netplotter

A real-time, terminal-based network path monitoring tool written in Go.

![netplotter](./netplotter.png)
---

## Features

- **Real-time traceroute** — discovers the path to a target and refreshes it periodically
- **Per-hop metrics** — latency (min/avg/max/last), packet loss %, jitter, and sample count
- **Sparkline graphs** — live ASCII bar chart of the last 20 RTT samples per hop
- **Loss graph** — red loss sparkline per hop
- **ANSI color coding** — green (healthy) / yellow (warning) / red (critical)
- **Windows kernel ICMP API** — uses `IcmpSendEcho` (iphlpapi.dll) on Windows, bypassing raw socket restrictions that block ICMP Time Exceeded messages
- **ICMP + TCP fallback** — automatically falls back to TCP if raw ICMP is unavailable
- **DNS resolution** — async reverse-DNS cache with 5-minute TTL
- **Route change detection** — notifies when the network path changes
- **Export** — JSON and CSV on a configurable interval
- **Multi-target panels** — monitor multiple targets in stacked panels
- **Diff view** — compare against a previous JSON export
- **Long-running** — circular buffers prevent memory growth; runs for days

---

## Requirements

| Platform | Requirement |
|----------|-------------|
| Windows  | Run as Administrator |
| Linux    | Run as `root` or grant `CAP_NET_RAW` |
| macOS    | Run with `sudo` |

> TCP mode (`--protocol tcp`) does **not** require elevated privileges but cannot probe intermediate hops.

---

## Build

```bash
# Requires Go 1.22+
go build -o netplotter ./cmd/netplotter

# Cross-compile for all platforms
GOOS=windows GOARCH=amd64 go build -o netplotter-windows-amd64.exe ./cmd/netplotter
GOOS=linux   GOARCH=amd64 go build -o netplotter-linux-amd64        ./cmd/netplotter
GOOS=darwin  GOARCH=amd64 go build -o netplotter-macos-amd64        ./cmd/netplotter
GOOS=darwin  GOARCH=arm64 go build -o netplotter-macos-arm64        ./cmd/netplotter
```

---

## Usage

```
netplotter [flags] [target]
```

If no target is provided, you will be prompted interactively:

```
$ ./netplotter
Target host or IP: 8.8.8.8
```

If `--targets` is provided, it overrides `--target` and the positional argument.

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--target` | Target hostname or IP | *(interactive prompt)* |
| `--targets` | Comma-separated targets | *(disabled)* |
| `--protocol` | `icmp` \| `tcp` \| `udp` | `icmp` |
| `--port` | Port for TCP/UDP probing | `80` |
| `--interval` | Probe interval (ms) | `1000` |
| `--timeout` | Probe timeout (ms) | `3000` |
| `--max-hops` | Maximum number of hops | `30` |
| `--buffer` | Samples per hop | `100` |
| `--probe-concurrency` | Max concurrent probes per target | `32` |
| `--route-refresh` | Route refresh interval (ms) | `60000` |
| `--render-interval` | Screen refresh interval (ms) | `250` |
| `--dns` | Reverse DNS resolution | `true` |
| `--warn-latency` | Warning latency threshold (ms) | `100` |
| `--critical-latency` | Critical latency threshold (ms) | `300` |
| `--warn-loss` | Warning packet loss threshold (0–1) | `0.05` |
| `--critical-loss` | Critical packet loss threshold (0–1) | `0.20` |
| `--export-json` | Export results to JSON file | *(disabled)* |
| `--export-csv` | Export results to CSV file | *(disabled)* |
| `--export-interval` | Export interval (ms) | `10000` |
| `--diff-file` | Compare against previous JSON export | *(disabled)* |
| `--no-color` | Disable ANSI color output | `false` |
| `--show-all` | Show hops with no response | `false` |
| `--panel-sort` | Panel sort: target, loss, avg | `target` |
| `--view` | View mode: avg, loss, all | `all` |
| `--adaptive` | Adaptive probing (experimental) | `false` |
| `--info` | Show developer info and exit | — |

---

## Examples

```bash
# Start without specifying a target (interactive prompt)
sudo ./netplotter

# Basic ICMP monitoring
sudo ./netplotter --target 8.8.8.8

# Positional argument (no --target flag needed)
sudo ./netplotter 1.1.1.1

# TCP mode — no elevated privileges required
./netplotter --target google.com --protocol tcp --port 443

# Multi-target panels
sudo ./netplotter --targets 8.8.8.8,1.1.1.1

# Fast probing with JSON export
sudo ./netplotter --target 1.1.1.1 --interval 500 --export-json /tmp/results.json

# Diff view (compare against a previous JSON export)
sudo ./netplotter --target 1.1.1.1 --diff-file /tmp/results.json

# More hops, no color
sudo ./netplotter --target 8.8.8.8 --max-hops 20 --no-color

# Show developer info
./netplotter --info
```

---

## Sample Output

```
netplotter — 8.8.8.8  │  uptime: 2m14s

Hop  IP Address        Hostname                     Loss%   Last    Avg     Min     Max     Jitter  Graph
─────────────────────────────────────────────────────────────────────────────────────────────────────────
  1  192.168.1.1       gateway.local                 0.0%   1.2ms   1.1ms   0.8ms   1.5ms   100µs  ▂▂▁▂▂▁▃▂
  2  10.0.1.1                                        0.0%   4.3ms   4.1ms   3.8ms   5.2ms   300µs  ▃▃▄▃▂▃▄▃
  3  72.14.204.33      a72-14-204-33.deploy.static   0.0%   8.7ms   8.5ms   8.0ms   9.2ms   400µs  ▄▄▄▅▄▄▄▄
  4  108.170.253.97                                  2.1%   9.1ms   9.3ms   8.8ms  11.4ms   800µs  ▄▄▅▄▄▄▄▅
  5  8.8.8.8           dns.google                    0.0%  10.2ms   9.8ms   9.1ms  11.0ms   500µs  ▄▄▄▄▄▅▄▄

Press Q to quit  │  total: 1500 sent, 1468 recv, 2.1% loss, 0 route changes
```

---

## Project Structure

```
netplotter/
├── cmd/netplotter/main.go              # Entry point, goroutine orchestration
├── internal/
│   ├── config/config.go               # CLI flag parsing and validation
│   ├── probe/
│   │   ├── prober.go                  # Prober interface
│   │   ├── icmp.go                    # Raw ICMP prober (Linux/macOS)
│   │   ├── icmp_windows.go            # Windows IcmpSendEcho API prober
│   │   ├── tcp.go                     # TCP connect prober (unprivileged)
│   │   ├── tcp_unix.go                # Unix TTL socket option
│   │   ├── tcp_windows.go             # Windows TTL stub
│   │   ├── factory.go                 # Prober factory (Linux/macOS)
│   │   └── factory_windows.go         # Prober factory (Windows)
│   ├── traceroute/
│   │   ├── hop.go                     # Hop type
│   │   └── traceroute.go              # Traceroute runner + route change detection
│   ├── metrics/
│   │   ├── buffer.go                  # Circular sample buffer
│   │   ├── hopmetrics.go              # Per-hop statistics
│   │   └── session.go                 # Session-level aggregation
│   ├── dns/resolver.go                # Async reverse-DNS with expiry cache
│   ├── renderer/
│   │   ├── renderer.go                # Renderer interface
│   │   ├── terminal.go                # ANSI terminal renderer + sparklines
│   │   └── ansi_windows.go            # Windows VT processing + UTF-8 code page
│   └── storage/
│       ├── storage.go                 # Exporter interface
│       ├── json.go                    # JSON exporter
│       └── csv.go                     # CSV exporter
└── Makefile
```

---

## License

MIT
