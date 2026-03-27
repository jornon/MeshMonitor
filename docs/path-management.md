# Path Management in MeshCore

How mesh routes are established, cached, and refreshed — and MeshMonitor's
strategy for detecting and correcting stale paths.

## How MeshCore Routing Works

MeshCore uses a **source-routed mesh** where each node caches an explicit path
(sequence of repeater hashes) to every known contact.

### Path Establishment

1. When `out_path_len == 0xFF` (unknown), messages are sent via **flood routing**.
2. Each repeater that forwards the flood appends its hash to the packet's path array.
3. The destination extracts the accumulated path, reverses it, and sends it back
   as a **path return** (bundled with the ACK/response).
4. The sender stores the received path in `out_path[]` for future direct sends.

### What Updates Paths

| Event | Updates path? | Updates name/GPS? | Updates LastAdvert? | Updates LastMod? |
|-------|--------------|-------------------|--------------------|--------------------|
| Path return received | **Yes** (overwrites) | No | No | Yes |
| Flood advert received | **No** | Yes | Yes | Yes |
| Zero-hop advert received | **No** | Yes | Yes | Yes |
| Text message received | No | No | No | Yes |
| CMD_RESET_PATH sent | Clears to unknown | No | No | No |

**Key insight:** Advertisements (flood or zero-hop) update metadata (name,
location, timestamps) but **never** update the cached `out_path`. A node that
moves and re-advertises will have a fresh name and GPS, but the monitoring
device will still try to reach it via the old (now invalid) path.

### What Doesn't Update Paths

- Flood adverts from a node that moved
- GPS coordinate changes
- Name changes
- Reboots (the path persists on the companion device)

## MeshMonitor's Stale Path Strategy

MeshMonitor detects and resets stale paths using two heuristics, implemented
in `refreshStalePaths()` in `main.go`.

### Heuristic 1: Path Max Age

If a contact's `LastMod` timestamp is older than **6 hours** (`pathMaxAge`),
the path is considered potentially outdated and is reset.

**Rationale:** In a dynamic mesh network, routes change as repeaters go offline,
come online, or get congested. A 6-hour window balances freshness against
unnecessary churn.

### Heuristic 2: Advert/Path Drift

If `LastAdvert` is more than **30 minutes** (`pathAdvertDrift`) newer than
`LastMod`, the contact has re-advertised (possibly from a new location or after
a reboot) without the path being refreshed.

**Rationale:** When a node re-advertises, its metadata (name, GPS) gets
updated on the companion device, but the cached route remains unchanged. If
the advert timestamp jumped forward significantly past the last path
modification, the route likely doesn't reflect reality.

**Note on clock domains:** `LastAdvert` uses the **remote node's RTC clock**
while `LastMod` uses the **local device's RTC clock**. Both should be
approximately in sync (MeshMonitor syncs the local clock via CMD_SET_DEVICE_TIME
at startup). The 30-minute threshold provides margin for clock drift.

### What Happens After Reset

1. `CMD_RESET_PATH (0x0D)` clears the cached path → `PathLen = -1` (unknown).
2. Contact list is re-fetched to reflect the cleared state.
3. The normal `discoverPaths()` flow picks up contacts with `PathLen == -1`.
4. Path discovery triggers flood routing, which establishes a fresh route.
5. Subsequent status/telemetry requests use the new direct path.

### Flow Diagram

```
Each monitoring cycle:
  1. Fetch contacts
  2. refreshStalePaths()     ← NEW
     - For each repeater with known path:
       - If LastMod > 6h old → CMD_RESET_PATH
       - If LastAdvert >> LastMod → CMD_RESET_PATH
     - Re-fetch contacts (reset paths now show PathLen=-1)
  3. discoverPaths()         ← existing
     - For each repeater with PathLen=-1:
       - CMD_PATH_DISCOVERY → triggers flood + path return
     - Re-fetch contacts (paths now updated)
  4. Poll repeaters for status/telemetry using fresh paths
```

## MeshCore Commands for Path Management

### CMD_RESET_PATH (0x0D)

Clears the cached `out_path` for a contact, setting `out_path_len = 0xFF`.
The next communication to this contact will flood, triggering a new path return.

```
TX: [0x0D][pub_key (32 bytes)]
RX: [0x00] (RESP_OK)
```

### CMD_PATH_DISCOVERY (0x34)

Initiates an active path discovery. The companion radio probes the mesh
for a route to the specified contact.

```
TX: [0x34][0x00][pub_key (32 bytes)]
RX: [0x00] (RESP_OK)
    ... later: [0x8D] (PUSH_PATH_DISCOVERY_RESP)
```

### CMD_REMOVE_CONTACT (0x0F)

Nuclear option — completely removes a contact from the device's table.
The contact must be re-discovered via advertisement.

```
TX: [0x0F][pub_key (32 bytes)]
RX: [0x00] (RESP_OK)
```

## Firmware Self-Healing

The MeshCore firmware has a built-in retry mechanism:

1. Direct send fails → retry up to 3 times
2. On the 3rd failure → reset path and flood on last retry
3. Flood triggers path return from destination → new path established

This provides self-healing for **actively communicating** contacts, but does
not help with monitoring contacts where the monitor initiates all communication.
MeshMonitor's `refreshStalePaths()` fills this gap for the monitoring use case.

## Configuration

The stale path thresholds are currently constants in `main.go`:

| Constant | Value | Description |
|----------|-------|-------------|
| `pathMaxAge` | 6 hours | Maximum age before path is reset |
| `pathAdvertDrift` | 30 minutes | Min gap between LastAdvert and LastMod to trigger reset |
| `discoveryCooldown` | 24 hours | Cooldown after failed discovery (avoids retry spam) |
| `discoveryStaleWindow` | 5 hours | Ignore contacts with adverts older than this |
