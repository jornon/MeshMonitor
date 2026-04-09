# MQTT Topic Format V2 — Monitor Attribution

## Overview

The server now supports a new MQTT topic format that includes the publishing monitor's
identity. This allows the server to track which monitor last reported telemetry for
each repeater — without relying on the payload (which the monitor could forge).

The old format continues to work. Monitors can be upgraded incrementally.

## Topic Format

### Old format (still supported)

```
{topic_prefix}/{repeater_pub_key_prefix}/{message_type}
```

Example: `meshmonitor/a1b2c3d4e5f6/status`

### New format (preferred)

```
{topic_prefix}/{monitor_pub_key_prefix}/{repeater_pub_key_prefix}/{message_type}
```

Example: `meshmonitor/deadbeef0123/a1b2c3d4e5f6/status`

Where:
- `topic_prefix` — from the config response (see below), e.g. `meshmonitor/deadbeef0123`
- `monitor_pub_key_prefix` — first 12 hex characters of the monitor's own public key
- `repeater_pub_key_prefix` — first 12 hex characters of the repeater's public key
- `message_type` — one of: `status`, `telemetry`, `neighbours`

## How to get the topic prefix

The `GET /api/v1/device/config` response now includes `topic_prefix` in the MQTT config:

```json
{
  "id": "monitor-uuid",
  "name": "My Monitor",
  "public_key": "deadbeef0123...",
  "mqtt": {
    "host": "mqtt.example.com",
    "port": 1883,
    "username": "monitor-<uuid>",
    "topic_prefix": "meshmonitor/deadbeef0123"
  }
}
```

**Use `mqtt.topic_prefix` as the base for all publish topics.** This replaces the
previous behavior of using a hardcoded or separately configured topic prefix.

### Publishing

For each repeater contact, publish to:

```
{mqtt.topic_prefix}/{repeater_pub_key_prefix}/status
{mqtt.topic_prefix}/{repeater_pub_key_prefix}/telemetry
{mqtt.topic_prefix}/{repeater_pub_key_prefix}/neighbours
```

Where `repeater_pub_key_prefix` is the first 12 hex chars of the repeater's public key
(lowercase).

## Example

Given:
- Monitor public key: `deadbeef0123456789abcdef...` (64 hex chars)
- Repeater public key prefix: `a1b2c3d4e5f6`
- Config returns `topic_prefix: "meshmonitor/deadbeef0123"`

Publish status to: `meshmonitor/deadbeef0123/a1b2c3d4e5f6/status`

## Payload formats

No changes to any payload formats. The `status`, `telemetry`, and `neighbours`
JSON payloads remain identical.

## ACL enforcement

The server creates a per-monitor MQTT role that restricts each monitor to only
publish under its own prefix (`meshmonitor/{its_prefix}/#`). A monitor cannot
publish to another monitor's topic subtree.

The shared `monitor` role (which allows `meshmonitor/#`) is still assigned for
backward compatibility with old-format topics. Once all monitors are upgraded, the
shared role can be tightened.

## Migration path

1. **Server deploys first** — subscribes to both old and new topic formats
2. **Monitors upgrade incrementally** — start using `mqtt.topic_prefix` from the
   config response instead of a hardcoded prefix
3. **Old monitors keep working** — messages on old-format topics are processed
   normally, but `last_seen_by` will be null for those repeaters
4. **After all monitors are upgraded** — the shared `monitor` role ACL can be
   restricted (optional, not urgent)

## What changes on the server

- Repeaters now have a `last_seen_by` field (monitor ID) in the API response
- The repeater list and detail endpoints include:
  ```json
  {
    "last_seen_at": "2026-04-09T12:00:00Z",
    "last_seen_by": {
      "id": "monitor-uuid",
      "name": "My Monitor",
      "public_key": "deadbeef0123..."
    }
  }
  ```
- `last_seen_by` is null if the repeater was last seen via an old-format topic

## Log collection

Monitors can publish diagnostic logs to the server via MQTT. This is controlled
by the `log_collection` flag in the config response.

### Config response

The config response now includes a `log_collection` field:

```json
{
  "id": "monitor-uuid",
  "name": "My Monitor",
  "public_key": "deadbeef0123...",
  "log_collection": true,
  "mqtt": {
    "host": "mqtt.example.com",
    "port": 1883,
    "username": "monitor-<uuid>",
    "topic_prefix": "meshmonitor/deadbeef0123"
  }
}
```

**Only publish logs when `log_collection` is `true`.**

### Topic

```
{mqtt.topic_prefix}/logs
```

Example: `meshmonitor/deadbeef0123/logs`

Unlike repeater topics, there is no repeater prefix — logs come from the
monitor itself.

### Payload

```json
{
  "logs": [
    {
      "timestamp": 1712678400,
      "level": "info",
      "tag": "mqtt",
      "message": "Connected to broker"
    },
    {
      "timestamp": 1712678401,
      "level": "error",
      "tag": "radio",
      "message": "TX timeout after 5s"
    }
  ]
}
```

Fields:
- `timestamp` — Unix epoch seconds (UTC)
- `level` — one of: `debug`, `info`, `warn`, `error`, `fatal`
- `tag` — subsystem label (max 64 chars), e.g. `mqtt`, `radio`, `wifi`, `sys`
- `message` — free-text log message (max 4096 chars)

Logs are batched in an array. Send when the buffer is full or on a timer (e.g.
every 30 seconds if there are pending entries).
