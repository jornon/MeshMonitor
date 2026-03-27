# CayenneLPP Telemetry Format

MeshCore repeaters encode sensor telemetry using the
[CayenneLPP](https://docs.mydevices.com/docs/lorawan/cayenne-lpp) format.
MeshMonitor decodes these in `cayenne.go` and publishes individual fields
to MQTT.

## Wire Format

Each reading is a TLV (Tag-Length-Value) triplet:

```
[channel (1 byte)] [type (1 byte)] [value (N bytes, big-endian)]
```

Readings are concatenated back-to-back with no separators.

## Supported Sensor Types

| Type | Name | Size | Unit | Scale |
|------|------|------|------|-------|
| `0x00` | Digital Input | 1 | 0/1 | — |
| `0x01` | Digital Output | 1 | 0/1 | — |
| `0x02` | Analog Input | 2 | V | ÷ 100 |
| `0x03` | Analog Output | 2 | V | ÷ 100 |
| `0x65` | Illuminance | 2 | lux | — |
| `0x66` | Presence | 1 | 0/1 | — |
| `0x67` | Temperature | 2 | C | ÷ 10 (signed) |
| `0x68` | Humidity | 1 | % | ÷ 2 |
| `0x73` | Barometer | 2 | hPa | ÷ 10 |
| `0x74` | Voltage | 2 | V | ÷ 100 |
| `0x75` | Current | 2 | A | ÷ 1000 |
| `0x76` | Frequency | 4 | Hz | — |
| `0x88` | GPS | 9 | lat/lon/alt | lat,lon ÷ 10000; alt ÷ 100 |

## Example

Raw hex: `0174017801670106`

Decoded:
- Channel 1 (`0x01`), Type `0x74` (Voltage): `0x0178` = 376 → 3.76V
- Channel 1 (`0x01`), Type `0x67` (Temperature): `0x0106` = 262 → 26.2C

## MQTT Output

Decoded fields are published individually in the telemetry JSON:

```json
{
  "name": "JornHjemme",
  "pub_key_prefix": "8b5445b0dde6",
  "cayenne_lpp": "0174017801670106",
  "ch1_voltage": 3.76,
  "ch1_temperature": 26.2
}
```

Field naming: `ch{channel}_{sensor_type}` (e.g. `ch1_voltage`, `ch2_humidity`).
