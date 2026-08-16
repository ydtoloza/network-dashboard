# Network Dashboard

A lightweight real-time network dashboard using vnStat and Go.

## Features

- **Live traffic**: per-interface RX/TX speed charts (rolling 60-sample window), updated every second.
- **Interface status**: online/offline indicator per interface (`operstate`).
- **Alert threshold**: highlight an interface in red when combined speed exceeds a configurable limit.
- **Historical records**: daily, hourly and monthly traffic from vnStat with range tabs (24h / 5d / 7d / 30d / monthly).
- **Summary cards**: total download/upload right now and for today.
- **Speed test**: on-demand download/upload/ping test against Cloudflare's public endpoints (≈10 s), showing the server's maximum connection speed.
- **Chart persistence**: the live chart survives page reloads via `localStorage`.
- **PWA**: installable with offline cache of the static shell.
- **Optional basic auth**, security headers, `/healthz` healthcheck, graceful shutdown.

## Endpoints

| Endpoint | Description |
| --- | --- |
| `/healthz` | Healthcheck (no auth required) |
| `/api/config` | Interfaces, poll interval, alert threshold, history size |
| `/api/realtime` | Live counters, server-computed speeds and rolling history per interface |
| `/api/history?range=24h\|5d\|7d\|30d\|month` | vnStat history for the requested range |
| `/api/summary` | Today's totals and current speeds per interface |
| `/api/speedtest?mb=200` | On-demand speed test: downloads/uploads up to `mb` MB per phase (~5 s each) against Cloudflare, returning `ping_ms`, `download`/`upload` `bps`. Returns `409` while another test runs |
| `/api/interfaces` | List of monitored interfaces (alias) |

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port |
| `INTERFACES` | `eth0` | Comma-separated interface names to monitor |
| `POLL_INTERVAL_MS` | `1000` | Backend sampling interval in milliseconds |
| `ALERT_THRESHOLD` | empty (off) | Alert when combined RX+TX speed exceeds this. Accepts `1048576`, `1M`, `500K`, `1.5G` |
| `AUTH_USER` / `AUTH_PASS` | empty (off) | Enables HTTP basic auth for the whole dashboard (except `/healthz`, `/sw.js`, `/manifest.webmanifest`) |
| `TZ` | - | Timezone for the container |

## Run

### Docker (recommended)

```bash
docker compose up -d --build
```

Then open http://localhost:8080. vnStat history is persisted in `./vnstat-data`.

### Locally

Requires Linux with `vnstat` installed and running:

```bash
vnstatd -n &
INTERFACES=eth0 go run .
```

## Development

```bash
go test -v ./...   # unit tests (no root required)
go vet ./...
gofmt -l .
```

## CI

`docker-publish.yml` runs format check, `go vet` and tests before building and
pushing the image to GHCR on pushes to `main` and version tags.
