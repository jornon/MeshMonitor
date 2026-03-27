# MeshCore Companion Radio Protocol

Reference documentation for the binary serial protocol between a companion app
(MeshMonitor) and a MeshCore device over USB-CDC.

Sources: [MeshCore wiki](https://github.com/meshcore-dev/MeshCore/wiki/Companion-Radio-Protocol),
[meshcore_py](https://github.com/meshcore-dev/meshcore_py), MeshCore firmware source.

## Frame Format

### USB/Serial Transport

All communication uses length-prefixed binary frames.

**Transmit (App to Device):**
```
0x3C  len_lo  len_hi  payload[0..len-1]
```

**Receive (Device to App):**
```
0x3E  len_lo  len_hi  payload[0..len-1]
```

- Length is little-endian 16-bit
- Maximum payload: 172 bytes
- Baud rate: 115200
- RTS must be de-asserted for ESP32 USB-CDC devices
- All multi-byte integers are **little-endian** unless noted

### BLE Transport

A single BLE characteristic value equals one frame. No marker/length header needed.

### Payload Routing

First byte of every payload:
- `0x00–0x7F` — response codes (solicited replies)
- `0x80–0xFF` — push codes (unsolicited notifications)

Current protocol version: **3**

---

## Command Codes (App to Device)

### Device Management

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x01` | CMD_APP_START | `[ver][reserved×6][app_name]` | RESP_SELF_INFO (0x05) |
| `0x05` | CMD_GET_DEVICE_TIME | (none) | RESP_CURR_TIME (0x09) |
| `0x06` | CMD_SET_DEVICE_TIME | `[unix_ts×4 LE]` | RESP_OK |
| `0x13` | CMD_REBOOT | `"reboot"` (ASCII, safety guard) | None (device reboots) |
| `0x14` | CMD_GET_BATT_AND_STORAGE | (none) | RESP_BATT_AND_STORAGE (0x0C) |
| `0x16` | CMD_DEVICE_QUERY | `[protocol_ver]` | RESP_DEVICE_INFO (0x0D) |
| `0x33` | CMD_FACTORY_RESET | `"reset"` (ASCII, safety guard) | None (device erases + reboots) |

### Contact Management

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x04` | CMD_GET_CONTACTS | `[since×4 LE]` (optional) | RESP_CONTACTS_START → N×RESP_CONTACT → RESP_END_OF_CONTACTS |
| `0x09` | CMD_ADD_UPDATE_CONTACT | Full 147-byte contact struct | RESP_OK |
| `0x0D` | CMD_RESET_PATH | `[pub_key×32]` | RESP_OK |
| `0x0F` | CMD_REMOVE_CONTACT | `[pub_key×32]` | RESP_OK |
| `0x10` | CMD_SHARE_CONTACT | `[pub_key×32]` | RESP_OK |
| `0x11` | CMD_EXPORT_CONTACT | `[pub_key×32]` (optional, omit=self) | RESP_EXPORT_CONTACT (0x0B) |
| `0x12` | CMD_IMPORT_CONTACT | `[card_data]` | RESP_OK |
| `0x1E` | CMD_GET_CONTACT_BY_KEY | `[pub_key×32]` | RESP_CONTACT (0x03) |
| `0x2A` | CMD_GET_ADVERT_PATH | `[0x00][pub_key×32]` | RESP_ADVERT_PATH (0x16) |

### Advertisement

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x07` | CMD_SEND_SELF_ADVERT | `[mode]` (0=zero-hop, 1=flood) | RESP_OK |
| `0x08` | CMD_SET_ADVERT_NAME | `[name UTF-8]` | RESP_OK |
| `0x0E` | CMD_SET_ADVERT_LATLON | `[lat×4 LE][lon×4 LE][alt×4 LE]` | RESP_OK |

### Messaging

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x02` | CMD_SEND_TXT_MSG | `[txt_type][attempt][ts×4][pubkey_prefix×6][text]` | RESP_SENT (0x06) |
| `0x03` | CMD_SEND_CHANNEL_MSG | `[txt_type][chan_idx][ts×4][text]` | RESP_OK |
| `0x0A` | CMD_SYNC_NEXT_MESSAGE | (none) | Message data or RESP_NO_MORE_MESSAGES (0x0A) |
| `0x19` | CMD_SEND_RAW_DATA | `[path_len][path][payload]` | RESP_SENT |

### Authentication

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x1A` | CMD_SEND_LOGIN | `[pub_key×32][password]` | RESP_SENT → PUSH_LOGIN_SUCCESS/FAILED |
| `0x1D` | CMD_LOGOUT | `[pub_key×32]` | RESP_OK |

### Binary Requests (preferred over legacy)

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x32` | CMD_BINARY_REQ | `[pub_key×32][req_type][data...]` | RESP_SENT → PUSH_BINARY_RESPONSE (0x8C) |
| `0x39` | CMD_SEND_ANON_REQ | `[pub_key×32][req_type][data...]` | RESP_SENT → PUSH_BINARY_RESPONSE |

**Binary request sub-types:**

| Type | Name | Extra Data | Description |
|------|------|------------|-------------|
| `0x01` | STATUS | (none) | Repeater stats (56-byte struct) |
| `0x02` | KEEP_ALIVE | (none) | Ping |
| `0x03` | TELEMETRY | (none) | CayenneLPP sensor data |
| `0x04` | MMA | `[start×4][end×4][reserved×2]` | Min/max/avg telemetry over time range |
| `0x05` | ACL | `[reserved×2]` | Access control list |
| `0x06` | NEIGHBOURS | `[ver][count][offset×2][order_by][prefix_len][tag×4]` | Neighbour list |

**Anonymous request sub-types:**

| Type | Name | Description |
|------|------|-------------|
| `0x01` | REGIONS | Region configuration |
| `0x02` | OWNER | Owner information |
| `0x03` | BASIC | Remote clock / basic info |

### Legacy Status/Telemetry (deprecated)

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x1B` | CMD_SEND_STATUS_REQ | `[pub_key×32]` | PUSH_STATUS_RESPONSE (0x87) |
| `0x27` | CMD_SEND_TELEMETRY_REQ | `[reserved×3][pub_key×32]` | PUSH_TELEMETRY_RESPONSE (0x8B) |

### Path Management

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x0D` | CMD_RESET_PATH | `[pub_key×32]` | RESP_OK |
| `0x34` | CMD_PATH_DISCOVERY | `[0x00][pub_key×32]` | RESP_SENT → PUSH_PATH_DISCOVERY_RESP (0x8D) |
| `0x24` | CMD_SEND_TRACE_PATH | `[tag×4][auth×4][flags][path]` | RESP_SENT → PUSH_TRACE_DATA (0x89) |
| `0x3D` | CMD_SET_PATH_HASH_MODE | `[0x00][mode]` (0=1B, 1=2B, 2=4B) | RESP_OK |

### Radio Configuration

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x0B` | CMD_SET_RADIO_PARAMS | `[freq×4][bw×4][sf][cr][repeat_mode]` | RESP_OK |
| `0x0C` | CMD_SET_RADIO_TX_POWER | `[power_dbm]` | RESP_OK |
| `0x15` | CMD_SET_TUNING_PARAMS | `[rxdelay×4][airtime_factor×4][reserved×8]` | RESP_OK |
| `0x2B` | CMD_GET_TUNING_PARAMS | (none) | RESP_TUNING_PARAMS (0x17) |
| `0x36` | CMD_SET_FLOOD_SCOPE | `[0x00][scope_key×16]` | RESP_OK |
| `0x3C` | CMD_GET_ALLOWED_REPEAT_FREQ | (none) | RESP_ALLOWED_REPEAT_FREQ (0x1A) |

### Channels

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x1F` | CMD_GET_CHANNEL | `[chan_idx]` | RESP_CHANNEL_INFO (0x12) |
| `0x20` | CMD_SET_CHANNEL | `[chan_idx][name×32][secret×16]` | RESP_OK |

### Signing

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x21` | CMD_SIGN_START | (none) | RESP_SIGN_START (0x13) |
| `0x22` | CMD_SIGN_DATA | `[chunk]` (~120 bytes) | RESP_OK |
| `0x23` | CMD_SIGN_FINISH | (none) | RESP_SIGNATURE (0x14) |

### Key Management

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x17` | CMD_EXPORT_PRIVATE_KEY | (none) | RESP_PRIVATE_KEY (0x0E) or RESP_DISABLED (0x0F) |
| `0x18` | CMD_IMPORT_PRIVATE_KEY | `[private_key×64]` | RESP_OK |

### Miscellaneous

| Code | Name | Payload | Response |
|------|------|---------|----------|
| `0x1C` | CMD_HAS_CONNECTION | (none) | RESP_OK |
| `0x25` | CMD_SET_DEVICE_PIN | `[ble_pin×4 LE]` | RESP_OK |
| `0x26` | CMD_SET_OTHER_PARAMS | `[manual_add][telemetry_modes][loc_policy][multi_acks]` | RESP_OK |
| `0x28` | CMD_GET_CUSTOM_VARS | (none) | RESP_CUSTOM_VARS (0x15) |
| `0x29` | CMD_SET_CUSTOM_VAR | `"name:value"` | RESP_OK |
| `0x37` | CMD_SEND_CONTROL_DATA | `[control_type][payload]` | RESP_OK |
| `0x38` | CMD_GET_STATS | (none) | RESP_STATS (0x18) |
| `0x3A` | CMD_SET_AUTOADD_CONFIG | `[flag]` | RESP_OK |
| `0x3B` | CMD_GET_AUTOADD_CONFIG | (none) | RESP_AUTOADD_CONFIG (0x19) |

---

## Response Codes (Device to App, < 0x80)

| Code | Name | Payload | Description |
|------|------|---------|-------------|
| `0x00` | RESP_OK | `[value×4 LE]` (optional) | Success |
| `0x01` | RESP_ERR | `[err_code]` | Error (see error codes below) |
| `0x02` | RESP_CONTACTS_START | `[count×4 LE]` | Start of contact list |
| `0x03` | RESP_CONTACT | 148-byte contact struct | One contact entry |
| `0x04` | RESP_END_OF_CONTACTS | `[most_recent_lastmod×4 LE]` | End of contact list |
| `0x05` | RESP_SELF_INFO | SelfInfo struct (variable) | Device identity |
| `0x06` | RESP_SENT | `[type][tag×4][suggested_timeout×4]` | Message queued. type: 0=direct, 1=flood |
| `0x07` | RESP_CONTACT_MSG_RECV | Legacy v2 message | Incoming direct message |
| `0x08` | RESP_CHANNEL_MSG_RECV | Legacy v2 channel message | Incoming channel message |
| `0x09` | RESP_CURR_TIME | `[epoch×4 LE]` | Device clock |
| `0x0A` | RESP_NO_MORE_MESSAGES | (none) | Message queue empty |
| `0x0B` | RESP_EXPORT_CONTACT | `[card_data]` | Business card data |
| `0x0C` | RESP_BATT_AND_STORAGE | `[mV×2][used_kb×4][total_kb×4]` | Battery and storage |
| `0x0D` | RESP_DEVICE_INFO | DeviceInfo struct | Firmware, model, capabilities |
| `0x0E` | RESP_PRIVATE_KEY | `[key×64]` | Exported private key |
| `0x0F` | RESP_DISABLED | (none) | Feature disabled |
| `0x10` | RESP_CONTACT_MSG_RECV_V3 | V3 message with SNR | Incoming message (protocol v3+) |
| `0x11` | RESP_CHANNEL_MSG_RECV_V3 | V3 channel message with SNR | Incoming channel message (v3+) |
| `0x12` | RESP_CHANNEL_INFO | `[idx][name×32][secret×16]` | Channel info |
| `0x13` | RESP_SIGN_START | `[reserved][max_length×4 LE]` | Signing session started |
| `0x14` | RESP_SIGNATURE | `[signature]` | Completed signature |
| `0x15` | RESP_CUSTOM_VARS | `"name:value,..."` | Custom variables |
| `0x16` | RESP_ADVERT_PATH | `[recv_ts×4][path_len][path]` | Advertisement path |
| `0x17` | RESP_TUNING_PARAMS | `[rxdelay×4][airtime_factor×4]` | Tuning parameters |
| `0x18` | RESP_STATS | `[sub_type][data]` | Stats (sub_type: 0=core, 1=radio, 2=packets) |
| `0x19` | RESP_AUTOADD_CONFIG | `[config]` | Auto-add setting |
| `0x1A` | RESP_ALLOWED_REPEAT_FREQ | Array of `[lo×4][hi×4]` pairs | Allowed frequencies |

### Error Codes (RESP_ERR)

| Code | Name |
|------|------|
| 1 | ERR_UNSUPPORTED_CMD |
| 2 | ERR_NOT_FOUND |
| 3 | ERR_TABLE_FULL |
| 4 | ERR_BAD_STATE |
| 5 | ERR_FILE_IO_ERROR |
| 6 | ERR_ILLEGAL_ARG |

---

## Push Codes (Device to App, >= 0x80)

| Code | Name | Payload | When Sent |
|------|------|---------|-----------|
| `0x80` | PUSH_ADVERT | `[pub_key×32]` | Advertisement received |
| `0x81` | PUSH_PATH_UPDATED | `[pub_key×32]` | Contact's path refreshed |
| `0x82` | PUSH_SEND_CONFIRMED | `[ack×4][round_trip_ms×4 LE]` | Message delivery confirmed |
| `0x83` | PUSH_MSG_WAITING | (none) | Messages queued for sync |
| `0x84` | PUSH_RAW_DATA | `[snr_x4][rssi][0xFF][payload]` | Raw data received |
| `0x85` | PUSH_LOGIN_SUCCESS | `[perms][pubkey_prefix×6][tag×4][new_perms]` | Auth succeeded |
| `0x86` | PUSH_LOGIN_FAILED | `[reserved][pubkey_prefix×6]` | Auth failed |
| `0x87` | PUSH_STATUS_RESPONSE | `[0x87][reserved][pubkey_prefix×6][stats×56]` | Legacy status |
| `0x88` | PUSH_LOG_DATA | `[snr_x4][rssi][raw_packet]` | RF packet log |
| `0x89` | PUSH_TRACE_DATA | `[reserved][path_len][flags][tag×4][auth×4][hashes][snrs]` | Path trace result |
| `0x8A` | PUSH_NEW_ADVERT | Full 148-byte contact struct | New contact (manual_add mode) |
| `0x8B` | PUSH_TELEMETRY_RESPONSE | `[0x8B][reserved][pubkey_prefix×6][CayenneLPP]` | Legacy telemetry |
| `0x8C` | PUSH_BINARY_RESPONSE | `[0x8C][reserved][tag×4][data]` | Binary request response |
| `0x8D` | PUSH_PATH_DISCOVERY_RESP | `[reserved][pubkey_prefix×6][out_path][in_path]` | Path discovery result |
| `0x8E` | PUSH_CONTROL_DATA | `[snr_x4][rssi][path_len][path][payload]` | Control packet received |
| `0x8F` | PUSH_CONTACT_DELETED | (none) | Contact removed |

---

## Data Structures

### Contact Struct (148 bytes)

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 1 | code | `0x03` |
| 1 | 32 | pub_key | Contact's public key |
| 33 | 1 | type | Advert type (1-4) |
| 34 | 1 | flags | Contact flags |
| 35 | 1 | path_len | `[7:6]`=hash_mode, `[5:0]`=hops. `0xFF`=unknown |
| 36 | 64 | out_path | Cached route (repeater hashes), zero-padded |
| 100 | 32 | name | Null-terminated |
| 132 | 4 | last_advert | Remote node's advert timestamp (LE) |
| 136 | 4 | lat | Latitude x 1e6, signed (LE) |
| 140 | 4 | lon | Longitude x 1e6, signed (LE) |
| 144 | 4 | lastmod | Local device's last-modified timestamp (LE) |

### RepeaterStats (56 bytes)

| Offset | Size | Type | Field |
|--------|------|------|-------|
| 0 | 2 | uint16 | batt_milli_volts |
| 2 | 2 | uint16 | tx_queue_len |
| 4 | 2 | int16 | noise_floor (dBm) |
| 6 | 2 | int16 | last_rssi (dBm) |
| 8 | 4 | uint32 | packets_recv |
| 12 | 4 | uint32 | packets_sent |
| 16 | 4 | uint32 | air_time_secs (TX) |
| 20 | 4 | uint32 | up_time_secs |
| 24 | 4 | uint32 | sent_flood |
| 28 | 4 | uint32 | sent_direct |
| 32 | 4 | uint32 | recv_flood |
| 36 | 4 | uint32 | recv_direct |
| 40 | 2 | uint16 | err_events |
| 42 | 2 | int16 | last_snr_x4 (divide by 4 for dB) |
| 44 | 2 | uint16 | direct_dups |
| 46 | 2 | uint16 | flood_dups |
| 48 | 4 | uint32 | rx_air_time_secs |
| 52 | 4 | uint32 | recv_errors |

### SelfInfo (variable length, min 36 bytes)

| Offset | Size | Field |
|--------|------|-------|
| 0 | 1 | code (`0x05`) |
| 1 | 1 | adv_type |
| 2 | 1 | tx_power |
| 3 | 1 | max_tx_power |
| 4 | 32 | pub_key |
| 36 | 4 | lat (LE, x1e6) |
| 40 | 4 | lon (LE, x1e6) |
| 44 | 1 | multi_acks |
| 45 | 1 | advert_loc_policy |
| 46 | 1 | telemetry_modes |
| 47 | 1 | manual_add_contacts |
| 48 | 4 | radio_freq (LE, Hz*1000) |
| 52 | 4 | radio_bw (LE, kHz*1000) |
| 56 | 1 | radio_sf |
| 57 | 1 | radio_cr |
| 58+ | var | name (null-terminated) |

### DeviceInfo

| Offset | Size | Field |
|--------|------|-------|
| 0 | 1 | code (`0x0D`) |
| 1 | 1 | firmware_ver |
| 2 | 2 | max_contacts (LE, multiply by 2 in v3+) |
| 4 | 1 | max_channels |
| 5 | 4 | ble_pin (LE) |
| 9 | 12 | firmware_build (null-term) |
| 21 | 40 | model (null-term) |
| 61 | 20 | version (null-term) |
| 81 | 1 | repeat_mode (v9+) |
| 82 | 1 | path_hash_mode (v10+) |

---

## Path Encoding

- **path_len byte:** bits `[5:0]` = hop count, bits `[7:6]` = hash_mode
- **hash_mode:** `hash_size = hash_mode + 1` (0→1 byte, 1→2 bytes, 2→4 bytes)
- **0xFF** = unknown/flood (no established path)
- Path stored in 64-byte `out_path` field, zero-padded
- Actual path data: `hop_count * hash_size` bytes

### How Paths Are Established

1. Unknown path → message sent via flood routing
2. Each forwarding repeater appends its hash to packet's path array
3. Destination reverses path and sends path return with ACK
4. Sender stores received path for future direct sends

### What Updates Paths

| Event | Updates out_path? | Updates name/GPS? | Updates LastAdvert? |
|-------|:-:|:-:|:-:|
| Path return received | Yes | No | No |
| Flood advert received | **No** | Yes | Yes |
| Zero-hop advert received | **No** | Yes | Yes |
| CMD_RESET_PATH | Clears | No | No |

See [path-management.md](path-management.md) for MeshMonitor's stale path strategy.

---

## Advert Types

| Value | Name |
|-------|------|
| 1 | Chat |
| 2 | Repeater |
| 3 | Room Server |
| 4 | Sensor |

## Text Types

| Value | Name |
|-------|------|
| 0 | PLAIN |
| 1 | CLI_DATA |
| 2 | SIGNED_PLAIN |

---

## MeshMonitor Implementation Status

Commands implemented by MeshMonitor:

| Status | Commands |
|--------|----------|
| **Used** | APP_START, GET_CONTACTS, SET_DEVICE_TIME, SEND_SELF_ADVERT, DEVICE_QUERY, SEND_LOGIN, LOGOUT, BINARY_REQ, PATH_DISCOVERY, RESET_PATH |
| **Defined only** | SYNC_NEXT_MESSAGE, SEND_STATUS_REQ (deprecated), SEND_TELEMETRY_REQ (deprecated), REMOVE_CONTACT |
| **Not implemented** | All other commands (~30) |
