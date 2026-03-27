package main

import (
	"encoding/binary"
	"time"
)

// ---------------------------------------------------------------------------
// Command codes (app → device)
// ---------------------------------------------------------------------------
const (
	CmdAppStart          = 0x01
	CmdSendTxtMsg        = 0x02
	CmdSendChannelMsg    = 0x03
	CmdGetContacts       = 0x04
	CmdGetDeviceTime     = 0x05
	CmdSetDeviceTime     = 0x06
	CmdSendSelfAdvert    = 0x07
	CmdSyncNextMessage   = 0x0A
	CmdResetPath         = 0x0D // decimal 13
	CmdRemoveContact     = 0x0F // decimal 15
	CmdGetBattAndStorage = 0x14 // decimal 20
	CmdDeviceQuery       = 0x16
	CmdSendLogin         = 0x1A // decimal 26
	CmdSendStatusReq     = 0x1B // decimal 27 (deprecated)
	CmdSendLogout        = 0x1D // decimal 29
	CmdSendTelemetryReq  = 0x27 // decimal 39 (deprecated)
	CmdBinaryReq         = 0x32 // decimal 50 — new binary request protocol
	CmdPathDiscovery     = 0x34 // decimal 52

	// Binary request sub-types (used with CmdBinaryReq)
	BinaryReqStatus     = 0x01
	BinaryReqTelemetry  = 0x03
	BinaryReqNeighbours = 0x06
)

// ---------------------------------------------------------------------------
// Response codes (device → app, code < 0x80)
// ---------------------------------------------------------------------------
const (
	RespCodeOK               = 0x00
	RespCodeErr              = 0x01
	RespCodeContactsStart    = 0x02
	RespCodeContact          = 0x03
	RespCodeEndOfContacts    = 0x04
	RespCodeSelfInfo         = 0x05
	RespCodeSent             = 0x06
	RespCodeNoMoreMessages   = 0x0A
	RespCodeBattAndStorage   = 0x0C
	RespCodeDeviceInfo       = 0x0D
)

// ---------------------------------------------------------------------------
// Push codes (device → app unsolicited, code >= 0x80)
// ---------------------------------------------------------------------------
const (
	PushCodeAdvert            = 0x80
	PushCodeSendConfirmed     = 0x82
	PushCodeMsgWaiting        = 0x83
	PushCodeLoginSuccess      = 0x85
	PushCodeLoginFailed       = 0x86
	PushCodeStatusResponse    = 0x87 // legacy
	PushCodeNewAdvert         = 0x8A
	PushCodeTelemetryResponse = 0x8B // legacy
	PushCodeBinaryResponse    = 0x8C
	PushCodePathDiscoveryResp = 0x8D
	PushCodeControlData       = 0x8E
	PushCodeContactDeleted    = 0x8F
	PushCodeContactsFull      = 0x90
)

// ---------------------------------------------------------------------------
// Contact / advert types
// ---------------------------------------------------------------------------
const (
	AdvTypeChat     = 1
	AdvTypeRepeater = 2
	AdvTypeRoom     = 3
	AdvTypeSensor   = 4
)

var AdvTypeNames = map[uint8]string{
	AdvTypeChat:     "Chat",
	AdvTypeRepeater: "Repeater",
	AdvTypeRoom:     "Room Server",
	AdvTypeSensor:   "Sensor",
}

// ---------------------------------------------------------------------------
// Frame builders
// ---------------------------------------------------------------------------

// BuildDeviceQuery returns the CMD_DEVICE_QUERY frame.
func BuildDeviceQuery() []byte {
	return []byte{CmdDeviceQuery, ProtocolVersion}
}

// BuildAppStart returns the CMD_APP_START frame.
// Layout: [0x01][version][reserved×6][app_name UTF-8]
func BuildAppStart(appName string) []byte {
	frame := []byte{CmdAppStart, ProtocolVersion, 0, 0, 0, 0, 0, 0}
	frame = append(frame, []byte(appName)...)
	return frame
}

// BuildSetDeviceTime returns the CMD_SET_DEVICE_TIME frame with the current Unix time.
func BuildSetDeviceTime() []byte {
	ts := uint32(time.Now().Unix())
	frame := make([]byte, 5)
	frame[0] = CmdSetDeviceTime
	binary.LittleEndian.PutUint32(frame[1:], ts)
	return frame
}

// BuildGetContacts returns the CMD_GET_CONTACTS frame.
func BuildGetContacts() []byte {
	return []byte{CmdGetContacts}
}

// BuildSendAdvert returns the CMD_SEND_SELF_ADVERT frame.
// flood=true sends a flood advertisement (visible to all), false sends zero-hop.
func BuildSendAdvert(flood bool) []byte {
	mode := byte(0)
	if flood {
		mode = 1
	}
	return []byte{CmdSendSelfAdvert, mode}
}

// BuildLogin returns the CMD_SEND_LOGIN frame.
// Layout: [0x1A][pub_key (32 bytes)][password UTF-8]
func BuildLogin(pubKey []byte, password string) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 33+len(password))
	frame[0] = CmdSendLogin
	copy(frame[1:33], pubKey)
	copy(frame[33:], []byte(password))
	return frame
}

// BuildLogout returns the CMD_SEND_LOGOUT frame.
// Layout: [0x1D][pub_key (32 bytes)]
func BuildLogout(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 33)
	frame[0] = CmdSendLogout
	copy(frame[1:], pubKey)
	return frame
}

// BuildResetPath returns the CMD_RESET_PATH frame.
// This clears the cached out_path for a contact, forcing the next
// communication to use flood routing and trigger a fresh path return.
// Layout: [0x0D][pub_key (32 bytes)]
func BuildResetPath(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 33)
	frame[0] = CmdResetPath
	copy(frame[1:], pubKey)
	return frame
}

// BuildPathDiscovery returns the CMD_PATH_DISCOVERY frame.
// Layout: [0x34][0x00][pub_key (32 bytes)]
func BuildPathDiscovery(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 34)
	frame[0] = CmdPathDiscovery
	frame[1] = 0x00
	copy(frame[2:], pubKey)
	return frame
}

// BuildBinaryReq returns a CMD_BINARY_REQ frame.
// Layout: [0x32][pub_key (32 bytes)][request_type (1 byte)]
func BuildBinaryReq(pubKey []byte, reqType byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 34)
	frame[0] = CmdBinaryReq
	copy(frame[1:33], pubKey)
	frame[33] = reqType
	return frame
}

// BuildNeighboursReq returns a neighbours request using the binary request protocol.
// Layout: [0x32][pub_key×32][0x06][version=0][count][offset×2 LE][order_by][prefix_len][random×4]
func BuildNeighboursReq(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 44)
	frame[0] = CmdBinaryReq
	copy(frame[1:33], pubKey)
	frame[33] = BinaryReqNeighbours
	frame[34] = 0x00 // version: must be 0
	frame[35] = 255  // count: request all available
	// offset[36:38] = 0 (first page, LE)
	frame[38] = 0                                        // order_by: 0=newest first
	frame[39] = 4                                        // pubkey_prefix_length: 4 bytes
	binary.LittleEndian.PutUint32(frame[40:44], rand32()) // random blob for uniqueness
	return frame
}

// rand32 returns a pseudo-random uint32.
func rand32() uint32 {
	return binary.LittleEndian.Uint32([]byte{
		byte(time.Now().UnixNano()),
		byte(time.Now().UnixNano() >> 8),
		byte(time.Now().UnixNano() >> 16),
		byte(time.Now().UnixNano() >> 24),
	})
}

// BuildStatusReq returns a status request using the binary request protocol.
func BuildStatusReq(pubKey []byte) []byte {
	return BuildBinaryReq(pubKey, BinaryReqStatus)
}

// BuildTelemetryReq returns a telemetry request using the binary request protocol.
func BuildTelemetryReq(pubKey []byte) []byte {
	return BuildBinaryReq(pubKey, BinaryReqTelemetry)
}
