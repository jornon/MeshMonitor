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
	CmdDeviceQuery       = 0x16
	CmdSendStatusReq     = 0x1B // decimal 27
	CmdSendTelemetryReq  = 0x27 // decimal 39
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
	PushCodeStatusResponse    = 0x87
	PushCodeNewAdvert         = 0x8A
	PushCodeTelemetryResponse = 0x8B
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

// BuildStatusReq returns the CMD_SEND_STATUS_REQ frame.
// Layout: [0x1B][pub_key (32 bytes)]
func BuildStatusReq(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 33)
	frame[0] = CmdSendStatusReq
	copy(frame[1:], pubKey)
	return frame
}

// BuildTelemetryReq returns the CMD_SEND_TELEMETRY_REQ frame.
// Layout: [0x27][reserved×3][pub_key (32 bytes)]
func BuildTelemetryReq(pubKey []byte) []byte {
	if len(pubKey) != 32 {
		panic("pubKey must be exactly 32 bytes")
	}
	frame := make([]byte, 36)
	frame[0] = CmdSendTelemetryReq
	// bytes 1-3: reserved (zero)
	copy(frame[4:], pubKey)
	return frame
}
