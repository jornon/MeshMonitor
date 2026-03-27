package main

import (
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

// Login authenticates with a repeater using the given guest password.
// Returns nil on success, error on failure or timeout.
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

	// Wait for LOGIN_SUCCESS or LOGIN_FAILED push.
	push, err := d.proto.WaitPush(PushCodeLoginSuccess, cfg.StatusTimeout)
	if err != nil {
		// Check if we got a LOGIN_FAILED instead.
		return fmt.Errorf("login failed or timed out: %w", err)
	}
	_ = push
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

// PathDiscovery sends a path discovery request for the given repeater.
// This triggers the mesh routing layer to find a path without requiring
// a full status exchange.
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
	return nil
}

// sendBinaryReq sends a binary request and returns the expected_ack tag from the SENT response.
func (d *Device) sendBinaryReq(frame []byte) ([]byte, error) {
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
	// RespCodeSent (0x06): [0x06][type 1][expected_ack 4][suggested_timeout 4]
	if resp[0] == RespCodeSent && len(resp) >= 6 {
		return resp[2:6], nil
	}
	return nil, nil
}

// waitBinaryResponse waits for a BINARY_RESPONSE push (0x8C) whose tag matches
// the expected_ack returned by sendBinaryReq. Responses with non-matching tags
// (late replies from previous requests) are discarded.
// Frame layout: [0x8C](1) [reserved](1) [tag](4) [response_data...]
func (d *Device) waitBinaryResponse(expectedTag []byte, timeout time.Duration) ([]byte, error) {
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
		if len(expectedTag) == 4 {
			tag := push[2:6]
			if tag[0] != expectedTag[0] || tag[1] != expectedTag[1] ||
				tag[2] != expectedTag[2] || tag[3] != expectedTag[3] {
				ui.Dimf("[device] discarding stale binary response (tag %x, want %x)\n", tag, expectedTag)
				continue
			}
		}
		return push[6:], nil
	}
	return nil, fmt.Errorf("timeout waiting for binary response")
}

// RequestStatus sends a status request to the given repeater (by 32-byte public key)
// and waits for the push response. Returns an error if no response arrives within the timeout.
func (d *Device) RequestStatus(pubKey []byte) (*StatusResponse, error) {
	tag, err := d.sendBinaryReq(BuildStatusReq(pubKey))
	if err != nil {
		return nil, fmt.Errorf("status request: %w", err)
	}

	data, err := d.waitBinaryResponse(tag, cfg.StatusTimeout)
	if err != nil {
		return nil, fmt.Errorf("status response: %w", err)
	}
	return ParseBinaryStatusResponse(data, pubKey[:6])
}

// RequestTelemetry sends a telemetry request to the given contact (by 32-byte public key)
// and waits for the push response.
func (d *Device) RequestTelemetry(pubKey []byte) (*TelemetryResponse, error) {
	tag, err := d.sendBinaryReq(BuildTelemetryReq(pubKey))
	if err != nil {
		return nil, fmt.Errorf("telemetry request: %w", err)
	}

	data, err := d.waitBinaryResponse(tag, cfg.TelemetryTimeout)
	if err != nil {
		return nil, fmt.Errorf("telemetry response: %w", err)
	}
	return ParseBinaryTelemetryResponse(data, pubKey[:6])
}
