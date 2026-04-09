# MeshMonitor

MeshCore repeater monitoring tool written in Go. Connects to a MeshCore companion
device over USB serial, polls repeaters for status/telemetry, reports to a central
server, and publishes data to MQTT.

## Build & run

```bash
make build              # builds ./meshmonitor (version from git tag)
make install            # installs to /usr/local/bin, config to /etc/meshmonitor
make enable start       # enable and start systemd service
make logs               # tail journal logs
./meshmonitor -v        # run locally with verbose output
```

Requires Go 1.24+ and `/usr/local/go/bin/go`.

## Project structure

| File                | Purpose                                              |
|---------------------|------------------------------------------------------|
| main.go             | Main loop: 15-min polling cycles, path discovery     |
| device.go           | High-level MeshCore device API (Init, GetContacts, RequestStatus, etc.) |
| protocol.go         | Low-level serial frame encoding/decoding, background reader goroutine |
| parser.go           | Binary frame parsing (SelfInfo, Contact, StatusResponse, TelemetryResponse) |
| commands.go         | Frame builders for all MeshCore protocol commands    |
| server.go           | HTTP API client (checkin, contacts, repeater list, config) |
| mqtt.go             | MQTT publisher (status + telemetry with CayenneLPP fields) |
| cayenne.go          | CayenneLPP sensor data decoder (11+ types)           |
| configfile.go       | INI config parsing, defaults, write-back             |
| config.go           | Config template generation                           |
| serial_detect.go    | USB port auto-detection with dialout group check     |
| update.go           | Auto-update from GitHub releases, version comparison |
| logbuf.go           | Thread-safe log ring buffer for remote collection    |
| ui.go               | ANSI-colored terminal output, spinners, tables       |
| cmd/debug/main.go   | Diagnostic tool for testing device communication     |

## Configuration

`meshmonitor.ini` (INI format, searched in working directory). On first run, an
interactive setup prompts for server URL, MQTT host, and auth token. Key sections:

- `[device]` — `serial_port` (auto-detected and persisted)
- `[timing]` — cycle intervals, timeouts, delays
- `[server]` — `url` (default: https://oslofjordmesh.no), `token` (required)
- `[mqtt]` — `host`, `port`, `topic_prefix`
- `[update]` — `auto_update`, `check_interval_mins`

## Key patterns

- All device commands are serialized via mutex (`device.go`)
- Background goroutine reads serial frames and routes to response/push channels (`protocol.go`)
- Repeater polling is hop-aware: near (0-1 hops) every 15 min, far (>1 hop) every 30 min
- Failed path discoveries are tracked in `discovery_failures.json` with 24h cooldown
- Server provides MQTT credentials and repeater target list each cycle
- Binary request/response protocol uses tag-based matching
- Auto-update checks GitHub releases periodically; downloads, replaces binary, and self-restarts
- Server-controlled log collection via MQTT (`logbuf.go`)

## Dependencies

- `go.bug.st/serial` — cross-platform serial I/O
- `github.com/eclipse/paho.mqtt.golang` — MQTT client

## Deployment

Runs as systemd service (`meshmonitor.service`). User must be in `dialout` group
for serial access. Config lives in `/etc/meshmonitor/` when installed.

## No tests

There are no automated tests. Validation is done via the `cmd/debug` tool and
live integration testing against MeshCore devices.
