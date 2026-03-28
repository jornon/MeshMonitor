package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

// DeviceReport is the payload POSTed to the central server.
type DeviceReport struct {
	Name         string  `json:"name"`
	PublicKey    string  `json:"public_key"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	Model        string  `json:"model,omitempty"`
	Firmware     string  `json:"firmware,omitempty"`
	RadioFreqKHz uint32  `json:"radio_freq_khz"`
	RadioBWKHz   uint32  `json:"radio_bw_khz"`
	RadioSF      uint8   `json:"radio_sf"`
	RadioCR      uint8   `json:"radio_cr"`
}

// ServerResponse is returned by the central server with repeaters to poll.
type ServerResponse struct {
	Repeaters []RepeaterTarget `json:"repeaters"`
}

// RepeaterTarget describes a repeater the monitor should collect data from.
type RepeaterTarget struct {
	Name               string `json:"name"`
	PublicKey          string `json:"public_key"` // hex, 64 chars (32 bytes)
	Hops               int    `json:"hops"`
	GuestPassword      string `json:"guest_password,omitempty"`
	CollectTemperature bool   `json:"collect_temperature"`
}

// DeviceConfig is returned by GET /api/v1/device/config with MQTT credentials.
type DeviceConfig struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	PublicKey string          `json:"public_key"`
	MQTT      DeviceConfigMQTT `json:"mqtt"`
}

// DeviceConfigMQTT holds the MQTT credentials from the config endpoint.
type DeviceConfigMQTT struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

// RepeaterContact is a repeater seen by this monitor, sent to the server
// so it can match monitors to nearby repeaters by hop count.
type RepeaterContact struct {
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Hops        int8   `json:"hops"`
	LastAdvert  uint32 `json:"last_advert"`
	LastMod     uint32 `json:"last_mod"`
}

// ---------------------------------------------------------------------------
// Server client
// ---------------------------------------------------------------------------

// PostDeviceReport sends the device identity and radio configuration to the
// server. Failure is non-fatal — the monitor can still fetch repeaters.
func PostDeviceReport(selfInfo *SelfInfo, devInfo *DeviceInfo) error {
	report := DeviceReport{
		Name:         selfInfo.Name,
		PublicKey:    selfInfo.PublicKeyHex,
		Lat:          selfInfo.Lat,
		Lon:          selfInfo.Lon,
		RadioFreqKHz: selfInfo.RadioFreqKHz,
		RadioBWKHz:   selfInfo.RadioBWKHz,
		RadioSF:      selfInfo.RadioSF,
		RadioCR:      selfInfo.RadioCR,
	}
	if devInfo != nil {
		report.Model = devInfo.Model
		report.Firmware = devInfo.Version
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal device report: %w", err)
	}

	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/device/checkin"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ServerToken)

	ui.Dimf("[server] POST %s\n", url)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST device report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// PostRepeaterContacts sends the list of repeaters visible to this monitor
// (with hop counts) to the server. Only repeaters are included.
func PostRepeaterContacts(contacts []*Contact) error {
	staleThreshold := uint32(time.Now().Unix()) - 72*3600 // 72 hours
	var repeaters []RepeaterContact
	for _, c := range contacts {
		if c.Type != AdvTypeRepeater {
			continue
		}
		if c.LastMod > 0 && c.LastMod < staleThreshold {
			continue
		}
		repeaters = append(repeaters, RepeaterContact{
			Name:       c.Name,
			PublicKey:  c.PublicKeyHex,
			Hops:       c.PathLen,
			LastAdvert: c.LastAdvert,
			LastMod:    c.LastMod,
		})
	}
	if len(repeaters) == 0 {
		return nil
	}

	wrapper := struct {
		Contacts []RepeaterContact `json:"contacts"`
	}{Contacts: repeaters}
	body, err := json.Marshal(wrapper)
	if err != nil {
		return fmt.Errorf("marshal repeater contacts: %w", err)
	}

	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/device/contacts"
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ServerToken)

	ui.Dimf("[server] PUT %s (%d repeaters)\n", url, len(repeaters))
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT repeater contacts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// FetchDeviceConfig retrieves the device configuration including MQTT credentials
// from GET /api/v1/device/config.
func FetchDeviceConfig() (*DeviceConfig, error) {
	if cfg.ServerToken == "" {
		return nil, fmt.Errorf("server.token is not set in config — cannot authenticate")
	}

	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/device/config"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ServerToken)
	req.Header.Set("Accept", "application/json")

	ui.Dimf("[server] GET %s\n", url)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET device config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed (401) — check server.token in config")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var devCfg DeviceConfig
	if err := json.Unmarshal(data, &devCfg); err != nil {
		return nil, fmt.Errorf("parse device config: %w", err)
	}
	return &devCfg, nil
}

// FetchRepeaters retrieves the list of repeaters this monitor should poll
// from GET /api/v1/device/repeaters, authenticated with the configured bearer token.
func FetchRepeaters() (*ServerResponse, error) {
	if cfg.ServerToken == "" {
		return nil, fmt.Errorf("server.token is not set in config — cannot authenticate")
	}

	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/device/repeaters"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ServerToken)
	req.Header.Set("Accept", "application/json")

	ui.Dimf("[server] GET %s\n", url)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET repeaters: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed (401) — check server.token in config")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var serverResp ServerResponse
	if err := json.Unmarshal(data, &serverResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &serverResp, nil
}
