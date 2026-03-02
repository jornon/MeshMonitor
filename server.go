package main

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

// DeviceReport is the payload POSTed to the central server.
type DeviceReport struct {
	Name      string            `json:"name"`
	PublicKey string            `json:"public_key"`
	Lat       float64           `json:"lat"`
	Lon       float64           `json:"lon"`
	Contacts  []ContactSnapshot `json:"contacts"`
}

// ContactSnapshot is a stripped-down view of a contact for the server payload.
type ContactSnapshot struct {
	Name      string  `json:"name"`
	PublicKey string  `json:"public_key"`
	Type      string  `json:"type"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
}

// ServerResponse is returned by the central server with repeaters to poll.
type ServerResponse struct {
	Repeaters []RepeaterTarget `json:"repeaters"`
}

// RepeaterTarget describes a repeater the monitor should collect data from.
type RepeaterTarget struct {
	Name      string  `json:"name"`
	PublicKey string  `json:"public_key"` // hex, 64 chars (32 bytes)
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
}

// ---------------------------------------------------------------------------
// Mocked server client
// ---------------------------------------------------------------------------

// PostDeviceReport sends device and contact info to the central server and
// returns the list of repeaters to poll. Currently MOCKED — no real HTTP call.
func PostDeviceReport(selfInfo *SelfInfo, contacts []*Contact) (*ServerResponse, error) {
	// Build request payload.
	report := DeviceReport{
		Name:      selfInfo.Name,
		PublicKey: selfInfo.PublicKeyHex,
		Lat:       selfInfo.Lat,
		Lon:       selfInfo.Lon,
	}
	for _, c := range contacts {
		report.Contacts = append(report.Contacts, ContactSnapshot{
			Name:      c.Name,
			PublicKey: c.PublicKeyHex,
			Type:      c.TypeName,
			Lat:       c.Lat,
			Lon:       c.Lon,
		})
	}

	payload, _ := json.MarshalIndent(report, "", "  ")
	ui.Dimf("[server] POST %s\n", cfg.ServerURL)
	ui.Dimf("[server] payload:\n%s\n", string(payload))

	// --- MOCK RESPONSE ---
	// In production this would be an actual HTTP POST.
	// We derive the repeater list from the contacts we already discovered,
	// filtering to ADV_TYPE_REPEATER entries.
	var repeaters []RepeaterTarget
	for _, c := range contacts {
		if c.Type == AdvTypeRepeater {
			repeaters = append(repeaters, RepeaterTarget{
				Name:      c.Name,
				PublicKey: c.PublicKeyHex,
				Lat:       c.Lat,
				Lon:       c.Lon,
			})
		}
	}

	resp := &ServerResponse{Repeaters: repeaters}
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	ui.Dimf("[server] mock response:\n%s\n", string(respJSON))

	if len(repeaters) == 0 {
		return resp, fmt.Errorf("server returned no repeaters to monitor")
	}
	return resp, nil
}
