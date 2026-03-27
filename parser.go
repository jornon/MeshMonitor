package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Data structures
// ---------------------------------------------------------------------------

// SelfInfo holds data from RESP_CODE_SELF_INFO (response to CMD_APP_START).
type SelfInfo struct {
	AdvType       uint8
	TxPower       uint8
	MaxTxPower    uint8
	PublicKey     []byte  // 32 bytes
	PublicKeyHex  string
	Lat           float64
	Lon           float64
	MultiACKs     uint8
	AdvLocPolicy  uint8
	TelemetryMode uint8
	RadioFreqKHz  uint32
	RadioBWKHz    uint32
	RadioSF       uint8
	RadioCR       uint8
	Name          string
}

// DeviceInfo holds data from RESP_CODE_DEVICE_INFO (response to CMD_DEVICE_QUERY).
type DeviceInfo struct {
	FirmwareVer uint8
	MaxContacts int
	MaxChannels int
	BLEPIN      uint32
	FirmwareBuild string
	Model         string
	Version       string
}

// Contact holds data from RESP_CODE_CONTACT.
type Contact struct {
	PublicKey    []byte // 32 bytes
	PublicKeyHex string
	Type         uint8
	TypeName     string
	Flags        uint8
	PathLen      int8 // -1 = unknown path
	Name         string
	LastAdvert   uint32
	Lat          float64
	Lon          float64
	LastMod      uint32
}

// StatusResponse holds data from PUSH_CODE_STATUS_RESPONSE.
// Contains the RepeaterStats struct from the repeater firmware.
type StatusResponse struct {
	PubKeyPrefix    string
	BattMilliVolts  uint16
	TxQueueLen      uint16
	NoiseFloor      int16
	LastRSSI        int16
	PacketsRecv     uint32
	PacketsSent     uint32
	AirTimeSecs     uint32
	UpTimeSecs      uint32
	SentFlood       uint32
	SentDirect      uint32
	RecvFlood       uint32
	RecvDirect      uint32
	ErrEvents       uint16
	LastSNRx4       int16 // divide by 4 for dB
	DirectDups      uint16
	FloodDups       uint16
	RxAirTimeSecs   uint32
	RecvErrors      uint32
}

// TelemetryResponse holds data from PUSH_CODE_TELEMETRY_RESPONSE.
type TelemetryResponse struct {
	PubKeyPrefix string
	RawData      []byte // CayenneLPP encoded
	RawHex       string
}

// ---------------------------------------------------------------------------
// Parsers
// ---------------------------------------------------------------------------

// ParseDeviceInfo parses a RESP_CODE_DEVICE_INFO frame.
func ParseDeviceInfo(frame []byte) (*DeviceInfo, error) {
	if len(frame) < 2 {
		return nil, fmt.Errorf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != RespCodeDeviceInfo {
		return nil, fmt.Errorf("unexpected response code: 0x%02X", frame[0])
	}
	info := &DeviceInfo{FirmwareVer: frame[1]}
	if info.FirmwareVer >= 3 && len(frame) >= 80 {
		info.MaxContacts = int(frame[2]) * 2
		info.MaxChannels = int(frame[3])
		info.BLEPIN = binary.LittleEndian.Uint32(frame[4:8])
		info.FirmwareBuild = nullTermStr(frame[8:20])
		info.Model = nullTermStr(frame[20:60])
		info.Version = nullTermStr(frame[60:80])
	}
	return info, nil
}

// ParseSelfInfo parses a RESP_CODE_SELF_INFO frame (response to CMD_APP_START).
// Fields beyond the public key are optional — older firmware may send a shorter frame.
// Minimum required: 36 bytes (code + adv_type + tx_power + max_tx_power + pub_key).
func ParseSelfInfo(frame []byte) (*SelfInfo, error) {
	if len(frame) < 36 {
		return nil, fmt.Errorf("self info frame too short: %d bytes (need at least 36)", len(frame))
	}
	if frame[0] != RespCodeSelfInfo {
		return nil, fmt.Errorf("unexpected response code: 0x%02X (want 0x%02X)", frame[0], RespCodeSelfInfo)
	}
	info := &SelfInfo{}
	info.AdvType = frame[1]
	info.TxPower = frame[2]
	info.MaxTxPower = frame[3]
	info.PublicKey = make([]byte, 32)
	copy(info.PublicKey, frame[4:36])
	info.PublicKeyHex = hex.EncodeToString(info.PublicKey)

	if len(frame) >= 44 {
		lat := int32(binary.LittleEndian.Uint32(frame[36:40]))
		lon := int32(binary.LittleEndian.Uint32(frame[40:44]))
		info.Lat = float64(lat) / 1e6
		info.Lon = float64(lon) / 1e6
	}
	if len(frame) >= 47 {
		info.MultiACKs = frame[44]
		info.AdvLocPolicy = frame[45]
		info.TelemetryMode = frame[46]
	}
	if len(frame) >= 58 {
		// frame[47] = manual_add_contacts (not stored)
		info.RadioFreqKHz = binary.LittleEndian.Uint32(frame[48:52])
		info.RadioBWKHz = binary.LittleEndian.Uint32(frame[52:56])
		info.RadioSF = frame[56]
		info.RadioCR = frame[57]
	}
	if len(frame) > 58 {
		info.Name = nullTermStr(frame[58:])
	}
	return info, nil
}

// ParseContact parses a RESP_CODE_CONTACT frame.
// Layout (total 148 bytes including code):
//   [0x03][pub_key×32][type][flags][path_len][path×64][name×32][last_advert×4][lat×4][lon×4][lastmod×4]
func ParseContact(frame []byte) (*Contact, error) {
	const minLen = 1 + 32 + 1 + 1 + 1 + 64 + 32 + 4 + 4 + 4 + 4 // 148
	if len(frame) < minLen {
		return nil, fmt.Errorf("contact frame too short: %d < %d", len(frame), minLen)
	}
	if frame[0] != RespCodeContact {
		return nil, fmt.Errorf("unexpected response code: 0x%02X", frame[0])
	}
	c := &Contact{}
	c.PublicKey = make([]byte, 32)
	copy(c.PublicKey, frame[1:33])
	c.PublicKeyHex = hex.EncodeToString(c.PublicKey)
	c.Type = frame[33]
	c.TypeName = AdvTypeNames[c.Type]
	if c.TypeName == "" {
		c.TypeName = fmt.Sprintf("Unknown(0x%02X)", c.Type)
	}
	c.Flags = frame[34]
	c.PathLen = int8(frame[35])
	// frame[36:100] = path (64 bytes, skipped)
	c.Name = nullTermStr(frame[100:132])
	c.LastAdvert = binary.LittleEndian.Uint32(frame[132:136])
	lat := int32(binary.LittleEndian.Uint32(frame[136:140]))
	lon := int32(binary.LittleEndian.Uint32(frame[140:144]))
	c.Lat = float64(lat) / 1e6
	c.Lon = float64(lon) / 1e6
	c.LastMod = binary.LittleEndian.Uint32(frame[144:148])
	return c, nil
}

// Legacy ParseStatusResponse and ParseTelemetryResponse removed.
// The binary request protocol (CMD_BINARY_REQ 0x32 → PUSH_BINARY_RESPONSE 0x8C)
// is used instead, with ParseBinaryStatusResponse and ParseBinaryTelemetryResponse.

// ParseBinaryStatusResponse parses status data from a binary response (0x8C).
// The data is the raw RepeaterStats struct with no header.
func ParseBinaryStatusResponse(data []byte, pubKeyPrefix []byte) (*StatusResponse, error) {
	if len(data) < 52 {
		return nil, fmt.Errorf("binary status data too short: %d bytes", len(data))
	}
	r := &StatusResponse{}
	r.PubKeyPrefix = hex.EncodeToString(pubKeyPrefix)

	rd := bytes.NewReader(data)
	binary.Read(rd, binary.LittleEndian, &r.BattMilliVolts)
	binary.Read(rd, binary.LittleEndian, &r.TxQueueLen)
	binary.Read(rd, binary.LittleEndian, &r.NoiseFloor)
	binary.Read(rd, binary.LittleEndian, &r.LastRSSI)
	binary.Read(rd, binary.LittleEndian, &r.PacketsRecv)
	binary.Read(rd, binary.LittleEndian, &r.PacketsSent)
	binary.Read(rd, binary.LittleEndian, &r.AirTimeSecs)
	binary.Read(rd, binary.LittleEndian, &r.UpTimeSecs)
	binary.Read(rd, binary.LittleEndian, &r.SentFlood)
	binary.Read(rd, binary.LittleEndian, &r.SentDirect)
	binary.Read(rd, binary.LittleEndian, &r.RecvFlood)
	binary.Read(rd, binary.LittleEndian, &r.RecvDirect)
	binary.Read(rd, binary.LittleEndian, &r.ErrEvents)
	binary.Read(rd, binary.LittleEndian, &r.LastSNRx4)
	binary.Read(rd, binary.LittleEndian, &r.DirectDups)
	binary.Read(rd, binary.LittleEndian, &r.FloodDups)
	binary.Read(rd, binary.LittleEndian, &r.RxAirTimeSecs)
	binary.Read(rd, binary.LittleEndian, &r.RecvErrors)
	return r, nil
}

// ParseBinaryTelemetryResponse parses telemetry data from a binary response (0x8C).
// The data is raw CayenneLPP bytes with no header.
func ParseBinaryTelemetryResponse(data []byte, pubKeyPrefix []byte) (*TelemetryResponse, error) {
	r := &TelemetryResponse{}
	r.PubKeyPrefix = hex.EncodeToString(pubKeyPrefix)
	if len(data) > 0 {
		r.RawData = make([]byte, len(data))
		copy(r.RawData, data)
		r.RawHex = hex.EncodeToString(r.RawData)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// nullTermStr reads a null-terminated (or null-padded) UTF-8 string from a byte slice.
func nullTermStr(b []byte) string {
	idx := bytes.IndexByte(b, 0)
	if idx >= 0 {
		b = b[:idx]
	}
	return strings.TrimSpace(string(b))
}
