package main

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// Device provides a high-level API for interacting with a MeshCore companion device.
// All commands are serialised through a mutex; push notifications are handled separately.
type Device struct {
	proto    *SerialProtocol
	mu       sync.Mutex
	SelfInfo *SelfInfo
	DevInfo  *DeviceInfo
}

// NewDevice opens a serial connection to the device on the given port.
func NewDevice(portName string) (*Device, error) {
	proto, err := NewSerialProtocol(portName)
	if err != nil {
		return nil, err
	}
	return &Device{proto: proto}, nil
}

// Close shuts down the device connection.
func (d *Device) Close() {
	d.proto.Close()
}

// Init performs the protocol handshake:
//  1. CMD_APP_START → RESP_CODE_SELF_INFO   (required)
//  2. CMD_DEVICE_QUERY → RESP_CODE_DEVICE_INFO (optional; older firmware may not support it)
//  3. CMD_SET_DEVICE_TIME                   (best-effort)
//
// Any stale data in the receive buffers is flushed before the handshake begins.
func (d *Device) Init() (*SelfInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Discard any stale frames from previous attempts or spontaneous device output.
	d.proto.Flush()

	// -----------------------------------------------------------------------
	// 1. APP_START — the device responds with its identity (SELF_INFO).
	//    Per the official protocol docs this must be the first command sent.
	// -----------------------------------------------------------------------
	ui.Dimf("[device] → CMD_APP_START\n")
	if err := d.proto.SendFrame(BuildAppStart(AppName)); err != nil {
		return nil, fmt.Errorf("send app start: %w", err)
	}
	resp, err := d.proto.WaitResponseCode(RespCodeSelfInfo, CommandResponseTimeout)
	if err != nil {
		return nil, fmt.Errorf("app start (self info): %w", err)
	}
	selfInfo, err := ParseSelfInfo(resp)
	if err != nil {
		return nil, fmt.Errorf("parse self info: %w", err)
	}
	d.SelfInfo = selfInfo
	ui.Dimf("[device] ← RESP_SELF_INFO  name=%q  key=%s...\n", selfInfo.Name, selfInfo.PublicKeyHex[:12])

	// -----------------------------------------------------------------------
	// 2. DEVICE_QUERY — optional; provides firmware version and capabilities.
	//    Not all firmware versions support this command; failure is non-fatal.
	// -----------------------------------------------------------------------
	ui.Dimf("[device] → CMD_DEVICE_QUERY\n")
	if err := d.proto.SendFrame(BuildDeviceQuery()); err == nil {
		resp, qErr := d.proto.WaitResponseCode(RespCodeDeviceInfo, CommandResponseTimeout)
		if qErr != nil {
			ui.Dimf("[device] device query not supported or timed out (%v) — continuing\n", qErr)
		} else if devInfo, pErr := ParseDeviceInfo(resp); pErr != nil {
			ui.Dimf("[device] device info parse error (%v) — continuing\n", pErr)
		} else {
			d.DevInfo = devInfo
			ui.Dimf("[device] ← RESP_DEVICE_INFO  fw=%d  model=%q  ver=%q\n",
				devInfo.FirmwareVer, devInfo.Model, devInfo.Version)
		}
	}

	// -----------------------------------------------------------------------
	// 3. SET_DEVICE_TIME — sync the device clock; best-effort.
	// -----------------------------------------------------------------------
	ui.Dimf("[device] → CMD_SET_DEVICE_TIME\n")
	if err := d.proto.SendFrame(BuildSetDeviceTime()); err == nil {
		_, _ = d.proto.WaitResponse(CommandResponseTimeout) // OK or timeout — both fine
	}

	return selfInfo, nil
}

// GetContacts retrieves all contacts from the device.
// Returns a slice of contacts (may be empty if none are stored).
func (d *Device) GetContacts() ([]*Contact, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.proto.SendFrame(BuildGetContacts()); err != nil {
		return nil, fmt.Errorf("send get contacts: %w", err)
	}

	// First response must be RESP_CODE_CONTACTS_START.
	resp, err := d.proto.WaitResponseCode(RespCodeContactsStart, CommandResponseTimeout)
	if err != nil {
		return nil, fmt.Errorf("contacts start: %w", err)
	}
	_ = resp // total count available at resp[1:5] if needed

	var contacts []*Contact
	for {
		resp, err = d.proto.WaitResponse(CommandResponseTimeout)
		if err != nil {
			// Timeout mid-stream — return what we have.
			break
		}
		switch resp[0] {
		case RespCodeContact:
			c, parseErr := ParseContact(resp)
			if parseErr == nil {
				contacts = append(contacts, c)
			} else {
				ui.Dimf("[device] contact parse error: %v\n", parseErr)
			}
		case RespCodeEndOfContacts:
			return contacts, nil
		case RespCodeErr:
			return contacts, fmt.Errorf("device returned error during contact list")
		}
	}
	return contacts, nil
}

// SendAdvert broadcasts a self-advertisement over the mesh network.
func (d *Device) SendAdvert() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.proto.SendFrame(BuildSendAdvert(true)); err != nil {
		return fmt.Errorf("send advert: %w", err)
	}
	_, _ = d.proto.WaitResponse(CommandResponseTimeout) // ignore OK/timeout
	return nil
}

// ---------------------------------------------------------------------------
// parseSentResponse extracts the tag and suggested timeout from RESP_SENT.
// Layout: [0x06][type 1][expected_ack 4][suggested_timeout 4]
// ---------------------------------------------------------------------------

type sentResponse struct {
	Tag              []byte
	SuggestedTimeout time.Duration
}

func parseSentResponse(resp []byte) *sentResponse {
	if len(resp) < 1 || resp[0] != RespCodeSent {
		return nil
	}
	sr := &sentResponse{}
	if len(resp) >= 6 {
		sr.Tag = resp[2:6]
	}
	if len(resp) >= 10 {
		ms := binary.LittleEndian.Uint32(resp[6:10])
		scaled := time.Duration(ms) * time.Millisecond / SuggestedTimeoutDivisor * 1000
		if scaled < MinAsyncTimeout {
			scaled = MinAsyncTimeout
		}
		sr.SuggestedTimeout = scaled
	} else {
		sr.SuggestedTimeout = cfg.StatusTimeout // fallback
	}
	return sr
}

// Login authenticates with a repeater using the given guest password.
// Returns nil on success, error on failure or timeout.
// Handles both LOGIN_SUCCESS (0x85) and LOGIN_FAILED (0x86) push codes.
func (d *Device) Login(pubKey []byte, password string) error {
	d.mu.Lock()
	err := d.proto.SendFrame(BuildLogin(pubKey, password))
	if err != nil {
		d.mu.Unlock()
		return fmt.Errorf("send login: %w", err)
	}
	resp, err := d.proto.WaitResponse(CommandResponseTimeout)
	d.mu.Unlock()

	if err != nil {
		return fmt.Errorf("login ack: %w", err)
	}
	if resp[0] == RespCodeErr {
		return fmt.Errorf("device rejected login command")
	}

	// Use suggested timeout from RESP_SENT if available.
	timeout := cfg.StatusTimeout
	if sr := parseSentResponse(resp); sr != nil && sr.SuggestedTimeout > 0 {
		timeout = sr.SuggestedTimeout
	}

	// Wait for LOGIN_SUCCESS or LOGIN_FAILED push.
	push, err := d.proto.WaitPushMulti(
		[]byte{PushCodeLoginSuccess, PushCodeLoginFailed},
		timeout,
	)
	if err != nil {
		return fmt.Errorf("login failed or timed out: %w", err)
	}
	if push[0] == PushCodeLoginFailed {
		return fmt.Errorf("login rejected by repeater")
	}
	return nil
}

// Logout ends the authenticated session with a repeater.
func (d *Device) Logout(pubKey []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = d.proto.SendFrame(BuildLogout(pubKey))
	_, _ = d.proto.WaitResponse(CommandResponseTimeout)
}

// ResetPath clears the cached path for a contact on the MeshCore device.
// The next communication to this contact will use flood routing, which
// triggers a fresh path return and establishes an up-to-date route.
func (d *Device) ResetPath(pubKey []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.proto.SendFrame(BuildResetPath(pubKey)); err != nil {
		return fmt.Errorf("send reset path: %w", err)
	}
	resp, err := d.proto.WaitResponse(CommandResponseTimeout)
	if err != nil {
		return fmt.Errorf("reset path ack: %w", err)
	}
	if resp[0] == RespCodeErr {
		return fmt.Errorf("device rejected reset path")
	}
	return nil
}

// PathDiscovery sends a path discovery request for the given repeater and
// waits for the PUSH_PATH_DISCOVERY_RESP to confirm the path was found.
func (d *Device) PathDiscovery(pubKey []byte) error {
	d.mu.Lock()
	err := d.proto.SendFrame(BuildPathDiscovery(pubKey))
	if err != nil {
		d.mu.Unlock()
		return fmt.Errorf("send path discovery: %w", err)
	}
	resp, err := d.proto.WaitResponse(CommandResponseTimeout)
	d.mu.Unlock()

	if err != nil {
		return fmt.Errorf("path discovery ack: %w", err)
	}
	if resp[0] == RespCodeErr {
		return fmt.Errorf("device rejected path discovery")
	}

	// Wait for the path discovery push response.
	timeout := cfg.StatusTimeout
	if sr := parseSentResponse(resp); sr != nil && sr.SuggestedTimeout > 0 {
		// Path discovery uses the most generous timeout scaling.
		timeout = sr.SuggestedTimeout * 4 / 3
	}
	_, pushErr := d.proto.WaitPush(PushCodePathDiscoveryResp, timeout)
	if pushErr != nil {
		return fmt.Errorf("path discovery response: %w", pushErr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Binary request protocol (status, telemetry, neighbours)
// ---------------------------------------------------------------------------

// sendBinaryReq sends a binary request and returns the parsed RESP_SENT data.
func (d *Device) sendBinaryReq(frame []byte) (*sentResponse, error) {
	d.mu.Lock()
	err := d.proto.SendFrame(frame)
	if err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("send binary req: %w", err)
	}
	resp, err := d.proto.WaitResponse(CommandResponseTimeout)
	d.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("binary req ack: %w", err)
	}
	if resp[0] == RespCodeErr {
		return nil, fmt.Errorf("device rejected request")
	}
	return parseSentResponse(resp), nil
}

// waitBinaryResponse waits for a BINARY_RESPONSE push (0x8C) whose tag matches
// the expected_ack returned by sendBinaryReq. Responses with non-matching tags
// (late replies from previous requests) are discarded.
// Frame layout: [0x8C](1) [reserved](1) [tag](4) [response_data...]
func (d *Device) waitBinaryResponse(sr *sentResponse, fallbackTimeout time.Duration) ([]byte, error) {
	timeout := fallbackTimeout
	if sr != nil && sr.SuggestedTimeout > 0 {
		timeout = sr.SuggestedTimeout
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		push, err := d.proto.WaitPush(PushCodeBinaryResponse, remaining)
		if err != nil {
			return nil, err
		}
		if len(push) < 6 {
			return nil, fmt.Errorf("binary response too short: %d bytes", len(push))
		}
		// If we have an expected tag, verify it matches.
		if sr != nil && len(sr.Tag) == 4 {
			tag := push[2:6]
			if tag[0] != sr.Tag[0] || tag[1] != sr.Tag[1] ||
				tag[2] != sr.Tag[2] || tag[3] != sr.Tag[3] {
				ui.Dimf("[device] discarding stale binary response (tag %x, want %x)\n", tag, sr.Tag)
				continue
			}
		}
		return push[6:], nil
	}
	return nil, fmt.Errorf("timeout waiting for binary response")
}

// RequestStatus sends a status request to the given repeater (by 32-byte public key)
// and waits for the push response. Retries up to MaxRequestRetries times, resetting
// the path on the final attempt to force flood routing.
func (d *Device) RequestStatus(pubKey []byte) (*StatusResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= MaxRequestRetries; attempt++ {
		// On the last attempt, reset path to force flood routing.
		if attempt == MaxRequestRetries {
			ui.Dimf("     🔄 Resetting path for retry %d\n", attempt)
			_ = d.ResetPath(pubKey)
		}

		sr, err := d.sendBinaryReq(BuildStatusReq(pubKey))
		if err != nil {
			lastErr = fmt.Errorf("status request: %w", err)
			continue
		}

		data, err := d.waitBinaryResponse(sr, cfg.StatusTimeout)
		if err != nil {
			lastErr = fmt.Errorf("status response: %w", err)
			if attempt < MaxRequestRetries {
				ui.Dimf("     🔄 Status attempt %d failed, retrying...\n", attempt)
			}
			continue
		}
		return ParseBinaryStatusResponse(data, pubKey[:6])
	}
	return nil, lastErr
}

// RequestTelemetry sends a telemetry request to the given contact (by 32-byte public key)
// and waits for the push response. Retries up to MaxRequestRetries times.
func (d *Device) RequestTelemetry(pubKey []byte) (*TelemetryResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= MaxRequestRetries; attempt++ {
		if attempt == MaxRequestRetries {
			_ = d.ResetPath(pubKey)
		}

		sr, err := d.sendBinaryReq(BuildTelemetryReq(pubKey))
		if err != nil {
			lastErr = fmt.Errorf("telemetry request: %w", err)
			continue
		}

		data, err := d.waitBinaryResponse(sr, cfg.TelemetryTimeout)
		if err != nil {
			lastErr = fmt.Errorf("telemetry response: %w", err)
			if attempt < MaxRequestRetries {
				ui.Dimf("     🔄 Telemetry attempt %d failed, retrying...\n", attempt)
			}
			continue
		}
		return ParseBinaryTelemetryResponse(data, pubKey[:6])
	}
	return nil, lastErr
}

// ---------------------------------------------------------------------------
// Neighbour discovery (#5)
// ---------------------------------------------------------------------------

// NeighbourEntry holds one neighbour from a NEIGHBOURS binary response.
type NeighbourEntry struct {
	PubKeyPrefix string
	SecsAgo      int32
	SNR          float64
}

// RequestNeighbours queries a repeater for its neighbour list.
func (d *Device) RequestNeighbours(pubKey []byte) ([]NeighbourEntry, error) {
	frame := BuildNeighboursReq(pubKey)
	sr, err := d.sendBinaryReq(frame)
	if err != nil {
		return nil, fmt.Errorf("neighbours request: %w", err)
	}

	data, err := d.waitBinaryResponse(sr, cfg.StatusTimeout)
	if err != nil {
		return nil, fmt.Errorf("neighbours response: %w", err)
	}
	entries, total, err := parseNeighboursResponse(data)
	if err != nil {
		return nil, err
	}
	ui.Dimf("[device] neighbours: %d/%d entries (raw first 64 bytes: %x)\n", len(entries), total, func() []byte {
		if len(data) > 64 {
			return data[:64]
		}
		return data
	}())
	return entries, nil
}

// parseNeighboursResponse parses the binary response from a NEIGHBOURS request.
// Layout: [neighbours_count×2 LE][results_count×2 LE][entries...]
// Each entry: [pubkey_prefix×prefixLen][secs_ago×4 LE][snr×1]
// Note: the tag/sender_timestamp is already stripped by waitBinaryResponse.
func parseNeighboursResponse(data []byte) ([]NeighbourEntry, int, error) {
	if len(data) < 4 {
		return nil, 0, fmt.Errorf("neighbours response too short: %d bytes", len(data))
	}
	totalCount := int(binary.LittleEndian.Uint16(data[0:2]))
	resultsCount := int(binary.LittleEndian.Uint16(data[2:4]))
	pos := 4
	prefixLen := 6 // must match what we requested in BuildNeighboursReq
	entrySize := prefixLen + 4 + 1

	var entries []NeighbourEntry
	for i := 0; i < resultsCount && pos+entrySize <= len(data); i++ {
		prefix := fmt.Sprintf("%x", data[pos:pos+prefixLen])
		pos += prefixLen
		secsAgo := int32(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		snr := float64(int8(data[pos])) / 4.0
		pos++
		entries = append(entries, NeighbourEntry{
			PubKeyPrefix: prefix,
			SecsAgo:      secsAgo,
			SNR:          snr,
		})
	}
	return entries, totalCount, nil
}

// ---------------------------------------------------------------------------
// Companion node stats (#6)
// ---------------------------------------------------------------------------

// CompanionStats holds local companion device statistics.
type CompanionStats struct {
	BattMilliVolts uint16
	UptimeSecs     uint32
	ErrFlags       uint16
	QueueLen       uint8
}

// GetBattAndStorage queries the companion node's battery and storage.
func (d *Device) GetBattAndStorage() (uint16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.proto.SendFrame([]byte{CmdGetBattAndStorage}); err != nil {
		return 0, fmt.Errorf("send batt query: %w", err)
	}
	resp, err := d.proto.WaitResponseCode(RespCodeBattAndStorage, CommandResponseTimeout)
	if err != nil {
		return 0, fmt.Errorf("batt response: %w", err)
	}
	if len(resp) < 3 {
		return 0, fmt.Errorf("batt response too short")
	}
	mv := binary.LittleEndian.Uint16(resp[1:3])
	return mv, nil
}
