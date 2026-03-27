package main

import (
	"encoding/binary"
	"fmt"
)

// CayenneLPP data type IDs
const (
	LPPDigitalInput  = 0
	LPPDigitalOutput = 1
	LPPAnalogInput   = 2
	LPPAnalogOutput  = 3
	LPPGenericSensor = 100
	LPPIlluminance   = 101
	LPPPresence      = 102
	LPPTemperature   = 103
	LPPHumidity      = 104
	LPPBarometer     = 115
	LPPVoltage       = 116
	LPPCurrent       = 117
	LPPFrequency     = 118
	LPPPercentage    = 120
	LPPGPS           = 136
)

// lppTypeSize returns the value size in bytes for a given LPP type.
// Returns 0 for unknown types.
func lppTypeSize(typeID byte) int {
	switch typeID {
	case LPPDigitalInput, LPPDigitalOutput, LPPPresence:
		return 1
	case LPPAnalogInput, LPPAnalogOutput, LPPTemperature, LPPHumidity,
		LPPVoltage, LPPCurrent, LPPFrequency, LPPPercentage,
		LPPIlluminance, LPPBarometer, LPPGenericSensor:
		return 2
	case LPPGPS:
		return 9
	default:
		return 0
	}
}

// CayenneValue holds a single decoded CayenneLPP value.
type CayenneValue struct {
	Channel  int     `json:"channel"`
	TypeName string  `json:"type"`
	Value    float64 `json:"value"`
}

// DecodeCayenneLPP decodes raw CayenneLPP bytes into a slice of values.
func DecodeCayenneLPP(data []byte) []CayenneValue {
	var values []CayenneValue
	i := 0
	for i < len(data) {
		if i+2 > len(data) {
			break
		}
		channel := data[i]
		typeID := data[i+1]
		i += 2

		size := lppTypeSize(typeID)
		if size == 0 || i+size > len(data) {
			break // unknown type or truncated
		}

		raw := data[i : i+size]
		i += size

		val, name := decodeLPPValue(typeID, raw)
		values = append(values, CayenneValue{
			Channel:  int(channel),
			TypeName: name,
			Value:    val,
		})
	}
	return values
}

func decodeLPPValue(typeID byte, raw []byte) (float64, string) {
	switch typeID {
	case LPPDigitalInput:
		return float64(raw[0]), "digital_input"
	case LPPDigitalOutput:
		return float64(raw[0]), "digital_output"
	case LPPAnalogInput:
		v := int16(binary.BigEndian.Uint16(raw))
		return float64(v) / 100.0, "analog_input"
	case LPPAnalogOutput:
		v := int16(binary.BigEndian.Uint16(raw))
		return float64(v) / 100.0, "analog_output"
	case LPPTemperature:
		v := int16(binary.BigEndian.Uint16(raw))
		return float64(v) / 10.0, "temperature"
	case LPPHumidity:
		v := binary.BigEndian.Uint16(raw)
		return float64(v) / 2.0, "humidity"
	case LPPVoltage:
		v := binary.BigEndian.Uint16(raw)
		val := float64(v) / 100.0
		// Handle signed wrap per meshcore convention
		if val > 327.67 {
			val -= 655.36
		}
		return val, "voltage"
	case LPPCurrent:
		v := binary.BigEndian.Uint16(raw)
		val := float64(v) / 1000.0
		if val > 32.767 {
			val -= 65.536
		}
		return val, "current"
	case LPPBarometer:
		v := binary.BigEndian.Uint16(raw)
		return float64(v) / 10.0, "barometer"
	case LPPIlluminance:
		v := binary.BigEndian.Uint16(raw)
		return float64(v), "illuminance"
	case LPPPresence:
		return float64(raw[0]), "presence"
	case LPPGenericSensor:
		v := binary.BigEndian.Uint16(raw)
		return float64(v), "generic_sensor"
	case LPPFrequency:
		v := binary.BigEndian.Uint16(raw)
		return float64(v), "frequency"
	case LPPPercentage:
		v := binary.BigEndian.Uint16(raw)
		return float64(v), "percentage"
	default:
		return 0, fmt.Sprintf("unknown_0x%02x", typeID)
	}
}

// DecodeCayenneGPS scans raw CayenneLPP data for a GPS entry (type 0x88).
// Returns [lat, lon, alt] or nil if no GPS data is present.
// GPS encoding: 3 bytes lat (signed BE, ÷10000), 3 bytes lon (signed BE, ÷10000),
// 3 bytes alt (signed BE, ÷100).
func DecodeCayenneGPS(data []byte) *[3]float64 {
	for i := 0; i+1 < len(data); {
		if i+2 > len(data) {
			break
		}
		// channel := data[i]
		typeID := data[i+1]
		size := lppTypeSize(typeID)
		if size == 0 {
			break
		}
		if i+2+size > len(data) {
			break
		}
		if typeID == LPPGPS && size == 9 {
			raw := data[i+2 : i+2+9]
			lat := float64(int32(raw[0])<<16|int32(raw[1])<<8|int32(raw[2])) / 10000.0
			if raw[0]&0x80 != 0 {
				lat = float64(int32(raw[0])<<16|int32(raw[1])<<8|int32(raw[2])-0x1000000) / 10000.0
			}
			lon := float64(int32(raw[3])<<16|int32(raw[4])<<8|int32(raw[5])) / 10000.0
			if raw[3]&0x80 != 0 {
				lon = float64(int32(raw[3])<<16|int32(raw[4])<<8|int32(raw[5])-0x1000000) / 10000.0
			}
			alt := float64(int32(raw[6])<<16|int32(raw[7])<<8|int32(raw[8])) / 100.0
			if raw[6]&0x80 != 0 {
				alt = float64(int32(raw[6])<<16|int32(raw[7])<<8|int32(raw[8])-0x1000000) / 100.0
			}
			return &[3]float64{lat, lon, alt}
		}
		i += 2 + size
	}
	return nil
}

// CayenneToMap converts decoded CayenneLPP values to a flat map suitable for
// MQTT publishing. Keys are the type name (e.g. "temperature", "voltage").
// If multiple channels have the same type, a suffix is added (e.g. "temperature_2").
func CayenneToMap(values []CayenneValue) map[string]float64 {
	m := make(map[string]float64)
	for _, v := range values {
		key := v.TypeName
		if _, exists := m[key]; exists {
			key = fmt.Sprintf("%s_%d", v.TypeName, v.Channel)
		}
		m[key] = v.Value
	}
	return m
}
