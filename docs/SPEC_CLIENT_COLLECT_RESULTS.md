# MeshMonitor Client Change — Collect Results Reporting

## Context

The server now assigns specific repeaters to each monitor for data collection. Assignments are distributed based on hop count (lowest wins) and load-balanced across monitors. When a monitor repeatedly fails

For the reassignment to work, the server needs the monitor to report which repeaters it successfully collected from and which it could not reach.

## API Changes

### Changed: `GET /api/v1/device/repeaters`

No change to the request or response format. The response still returns the same JSON shape:

```json
{
  "repeaters": [
    {
      "name": "Hilltop Solar",
      "public_key": "a1b2c3...64 hex chars",
      "hops": 1,
      "guest_password": "optional",
      "collect_temperature": true,
      "has_solar": true
    }
  ]
}
```

**Behavioral change:** The list is now filtered by the server's assignment algorithm. The monitor may receive fewer repeaters than it has discovered via mesh — only the ones the server has assigned to it. Ot

The client should treat this list as authoritative: only collect data from repeaters in this list.

### New: `PUT /api/v1/device/collect-results`

After each collection cycle, report results for every repeater that was in the assignment list.

**Authentication:** Same monitor token header as all `/device/*` endpoints.

**Request:**

```json
PUT /api/v1/device/collect-results
Content-Type: application/json
Authorization: Bearer <monitor-token>

{
  "results": [
    { "public_key": "a1b2c3d4e5f6...64 hex chars", "success": true },
    { "public_key": "f6e5d4c3b2a1...64 hex chars", "success": false }
  ]
}
```

| Field | Type | Validation | Description |
|-------|------|------------|-------------|
| `results` | array | required, non-empty | One entry per repeater attempted |
| `results[].public_key` | string | required, exactly 64 hex chars | The repeater's public key |
| `results[].success` | boolean | required | `true` if data was collected and published to MQTT, `false` if the repeater was unreachable |

**Response:** `204 No Content` on success.

**Error responses:**
- `400` — malformed request body
- `401` — invalid or missing monitor token

## Required Client Behavior

### Collection cycle (updated flow)

```
1. GET  /device/repeaters          → receive assigned repeater list
2. For each repeater in the list:
     - Attempt to collect status/telemetry via mesh
     - Publish to MQTT on success
     - Record success or failure
3. PUT  /device/collect-results    → report results to server
```

### What counts as success vs failure

- **Success:** The monitor received a status or telemetry response from the repeater and published it to MQTT.
- **Failure:** The monitor sent a request to the repeater but received no response within the expected timeout, or the request could not be sent (e.g., repeater not reachable on mesh).

### Timing

Call `PUT /device/collect-results` once per collection cycle, after all repeaters in the current batch have been attempted. There is no requirement on how frequently the collection cycle runs — the existing cycle interval is fine.

### Partial results

It is acceptable to report partial results (e.g., only failures, or only a subset of repeaters). However, reporting complete results for every assigned repeater gives the server the best information for reassignment decisions.

### What happens server-side

- On `success: true` — the server resets the failure counter for that repeater.
- On `success: false` — the server increments the failure counter. After 3 consecutive failures, the repeater is reassigned to the next-closest monitor (by hop count). The reassigned repeater will no longer appear in this monitor's `/device/repeaters` response.

## No other changes required

- `POST /device/checkin` — unchanged
- `GET /device/config` — unchanged
- `PUT /device/contacts` — unchanged
- MQTT publishing — unchanged
