# MeshMonitor

A monitoring tool for [MeshCore](https://meshcore.co) repeater networks. MeshMonitor connects to a MeshCore companion device over USB serial, periodically polls repeaters for status and telemetry data, reports to a central server, and publishes results to an MQTT broker.

## Features

- Auto-detects USB-connected MeshCore companion devices
- Polls repeaters for status (battery, signal, packet counts, uptime) and telemetry (CayenneLPP sensor data)
- Hop-aware scheduling: near repeaters (0-1 hops) every 15 min, far repeaters every 30 min
- Path discovery for repeaters with unknown hop counts
- Server-coordinated repeater target lists
- MQTT publishing for integration with Home Assistant, Node-RED, etc.
- Guest login support for password-protected repeaters
- Neighbour/topology discovery for mesh mapping
- GPS coordinates in status and telemetry messages
- Server-controlled log collection for remote diagnostics
- Automatic self-update from GitHub releases
- Colored terminal output with live countdown and spinners

## Requirements

- Go 1.24+
- A MeshCore companion device (ESP32 USB-CDC)
- Linux with `dialout` group membership for serial access

## Quick start

```bash
# Build
make build

# Run interactively (first run prompts for server URL, MQTT, and auth token)
./meshmonitor

# Run with verbose output
./meshmonitor -v
```

On first run, MeshMonitor will:
1. Auto-detect your USB serial device (or prompt you to connect one)
2. Ask for your server URL and authentication token
3. Display your device's public key for server-side registration
4. Save all settings to `meshmonitor.ini`

## Installation (systemd)

```bash
make install        # build + install binary to /usr/local/bin, config to /etc/meshmonitor
make enable start   # enable and start the service
make logs           # follow journal output
```

Other make targets: `stop`, `restart`, `status`, `uninstall`.

## Configuration

All settings are in `meshmonitor.ini` (INI format). A default template with all options documented is created on first run. Key settings:

```ini
[device]
serial_port = /dev/ttyACM0      # auto-detected on first run

[server]
url = https://oslofjordmesh.no  # central API endpoint
token =                         # bearer token (required)

[mqtt]
host = mqtt.oslofjordmesh.no    # MQTT broker
port = 1883
topic_prefix = meshmonitor      # topics: <prefix>/<pubkey_prefix>/status|telemetry

[timing]
; See meshmonitor.ini for all timing options (cycle interval, timeouts, delays)

[update]
auto_update = true              # check GitHub for new releases
; check_interval_mins = 60      # how often to check (default: 60)
```

## MQTT topics

The topic prefix is provided by the server during config sync. MeshMonitor publishes to:

- `<prefix>/<pubkey_prefix>/status` - repeater status (battery, RSSI, SNR, uptime, GPS, etc.)
- `<prefix>/<pubkey_prefix>/telemetry` - sensor data (temperature, humidity, voltage, GPS, etc.)
- `<prefix>/<pubkey_prefix>/neighbours` - mesh neighbour list with signal strengths
- `<prefix>/companion/status` - companion device battery and info
- `<prefix>/logs` - diagnostic log entries (when server-enabled)

Telemetry is decoded from CayenneLPP format with individual fields published for easy integration.

## Architecture

```
USB Serial <-> protocol.go (frame encode/decode, background reader)
                   |
               device.go (high-level MeshCore API)
                   |
               main.go (15-min polling cycle orchestration)
                  / \
         server.go   mqtt.go
         (HTTP API)  (MQTT publish)
```

The polling cycle:
1. Initialize device, sync time, send advertisement
2. Fetch contacts from device, sync with server
3. Get repeater target list from server
4. Poll each repeater for status and telemetry (with guest login if needed)
5. Publish results to MQTT
6. Wait for next cycle

## Debug tool

A diagnostic utility is included for testing device communication:

```bash
go run ./cmd/debug
```
