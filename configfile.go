package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// AppConfig — all runtime-configurable settings
// ---------------------------------------------------------------------------

// AppConfig holds every setting that can be overridden via meshmonitor.ini.
// The zero value is not valid; always obtain one via defaultConfig() or LoadConfig().
type AppConfig struct {
	// [device]
	SerialPort string // pre-set serial port; skips auto-detection when non-empty

	// [timing]
	CycleInterval          time.Duration
	MinDelayBetweenReqs    time.Duration
	MaxDelayBetweenReqs    time.Duration
	AdvertWait             time.Duration
	StatusTimeout          time.Duration
	TelemetryTimeout       time.Duration
	PortDetectTimeout      time.Duration

	// [server]
	ServerURL   string
	ServerToken string // Bearer token for API authentication

	// [mqtt]
	MQTTHost        string
	MQTTPort        int
	MQTTTopicPrefix string
}

func defaultConfig() *AppConfig {
	return &AppConfig{
		CycleInterval:       5 * time.Minute,
		MinDelayBetweenReqs: 1 * time.Second,
		MaxDelayBetweenReqs: 3 * time.Second,
		AdvertWait:          30 * time.Second,
		StatusTimeout:       15 * time.Second,
		TelemetryTimeout:    15 * time.Second,
		PortDetectTimeout:   60 * time.Second,
		ServerURL:           "https://mesh.jorno.org",
		MQTTHost:            "mqtt.jorno.org",
		MQTTPort:            1883,
		MQTTTopicPrefix:     "meshmonitor",
	}
}

// cfg is the active configuration, initialised to defaults and optionally
// overridden by the INI file at startup.
var cfg = defaultConfig()

// ---------------------------------------------------------------------------
// Config file path
// ---------------------------------------------------------------------------

const configFileName = "meshmonitor.ini"

// ConfigPath returns the path of the config file (same directory as the binary,
// falling back to the current working directory when the binary path is unavailable).
func ConfigPath() string {
	return configFileName
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// LoadConfig reads meshmonitor.ini and overrides the defaults in cfg.
// Commented-out lines (starting with ;) are silently ignored, so the file
// can ship with every option pre-documented but inactive.
// Returns nil if the file does not exist (defaults remain in effect).
func LoadConfig(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil // no config file — defaults are fine
	}
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header.
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line[1 : len(line)-1])
			continue
		}

		// key = value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		applyConfigKey(section, key, val)
	}
	return scanner.Err()
}

func applyConfigKey(section, key, val string) {
	switch section + "." + key {
	case "device.serial_port":
		cfg.SerialPort = val

	case "timing.cycle_interval_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.CycleInterval = time.Duration(s * float64(time.Second))
		}
	case "timing.min_delay_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.MinDelayBetweenReqs = time.Duration(s * float64(time.Second))
		}
	case "timing.max_delay_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.MaxDelayBetweenReqs = time.Duration(s * float64(time.Second))
		}
	case "timing.advert_wait_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.AdvertWait = time.Duration(s * float64(time.Second))
		}
	case "timing.status_timeout_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.StatusTimeout = time.Duration(s * float64(time.Second))
		}
	case "timing.telemetry_timeout_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TelemetryTimeout = time.Duration(s * float64(time.Second))
		}
	case "timing.port_detect_timeout_secs":
		if s, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.PortDetectTimeout = time.Duration(s * float64(time.Second))
		}

	case "server.url":
		cfg.ServerURL = val
	case "server.token":
		cfg.ServerToken = val

	case "mqtt.host":
		cfg.MQTTHost = val
	case "mqtt.port":
		if p, err := strconv.Atoi(val); err == nil {
			cfg.MQTTPort = p
		}
	case "mqtt.topic_prefix":
		cfg.MQTTTopicPrefix = val
	}
}

// ---------------------------------------------------------------------------
// Write-back — persist the detected serial port
// ---------------------------------------------------------------------------

// SaveSerialPort writes the detected port into the [device] serial_port key of
// the config file.  If the file does not exist it is created from the default
// template first.  Any existing (commented or uncommented) serial_port line is
// replaced so the value takes effect on the next run.
func SaveSerialPort(path, port string) error {
	// Ensure the file exists with the full template before modifying it.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := WriteDefaultConfig(path); err != nil {
			return err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config for update: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	targetLine := "serial_port = " + port
	replaced := false

	for i, line := range lines {
		stripped := strings.TrimLeft(strings.TrimSpace(line), ";# \t")
		if strings.HasPrefix(stripped, "serial_port") {
			lines[i] = targetLine
			replaced = true
			break
		}
	}

	if !replaced {
		// Not found — insert right after the [device] section header.
		for i, line := range lines {
			if strings.TrimSpace(line) == "[device]" {
				rest := make([]string, len(lines)-i-1)
				copy(rest, lines[i+1:])
				lines = append(lines[:i+1], append([]string{targetLine}, rest...)...)
				replaced = true
				break
			}
		}
	}
	if !replaced {
		// No [device] section found — append.
		lines = append(lines, "", "[device]", targetLine)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// SaveToken writes the given token into the [server] token key of the config
// file, creating the file from the default template first if needed.
func SaveToken(path, token string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := WriteDefaultConfig(path); err != nil {
			return err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config for update: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	targetLine := "token = " + token
	replaced := false

	for i, line := range lines {
		stripped := strings.TrimLeft(strings.TrimSpace(line), ";# \t")
		if strings.HasPrefix(stripped, "token") {
			lines[i] = targetLine
			replaced = true
			break
		}
	}

	if !replaced {
		// Insert after the [server] section header.
		for i, line := range lines {
			if strings.TrimSpace(line) == "[server]" {
				rest := make([]string, len(lines)-i-1)
				copy(rest, lines[i+1:])
				lines = append(lines[:i+1], append([]string{targetLine}, rest...)...)
				replaced = true
				break
			}
		}
	}
	if !replaced {
		lines = append(lines, "", "[server]", targetLine)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// ---------------------------------------------------------------------------
// Default template
// ---------------------------------------------------------------------------

// WriteDefaultConfig creates a fully-documented, all-commented-out config file.
// Existing files are not overwritten.
func WriteDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return os.WriteFile(path, []byte(defaultConfigTemplate()), 0644)
}

func defaultConfigTemplate() string {
	d := defaultConfig()
	return fmt.Sprintf(`; MeshMonitor configuration file
; Lines starting with ; are comments — the setting is inactive and the
; built-in default is used.  Remove the ; to activate a setting.

[device]
; Serial port to connect to.
; When set, the USB auto-detection wizard is skipped on startup.
; This value is written automatically when a device is first detected.
; serial_port =

[timing]
; Seconds between full monitoring cycles (default: %.0f)
; cycle_interval_secs = %.0f

; Minimum seconds between consecutive repeater requests (default: %.1f)
; min_delay_secs = %.1f

; Maximum seconds between consecutive repeater requests (default: %.1f)
; max_delay_secs = %.1f

; Seconds to wait after sending an advertisement before re-checking
; for contacts (default: %.0f)
; advert_wait_secs = %.0f

; Seconds to wait for a status response from a repeater (default: %.0f)
; status_timeout_secs = %.0f

; Seconds to wait for a telemetry response from a repeater (default: %.0f)
; telemetry_timeout_secs = %.0f

; Seconds to wait for the USB port to appear during auto-detection (default: %.0f)
; port_detect_timeout_secs = %.0f

[server]
; Central server base URL
; url = %s

; Bearer token for API authentication (required)
; token =

[mqtt]
; MQTT broker hostname
; host = %s

; MQTT broker port
; port = %d

; MQTT topic prefix  (topics: <prefix>/<pubkey_prefix>/status|telemetry)
; topic_prefix = %s
`,
		d.CycleInterval.Seconds(), d.CycleInterval.Seconds(),
		d.MinDelayBetweenReqs.Seconds(), d.MinDelayBetweenReqs.Seconds(),
		d.MaxDelayBetweenReqs.Seconds(), d.MaxDelayBetweenReqs.Seconds(),
		d.AdvertWait.Seconds(), d.AdvertWait.Seconds(),
		d.StatusTimeout.Seconds(), d.StatusTimeout.Seconds(),
		d.TelemetryTimeout.Seconds(), d.TelemetryTimeout.Seconds(),
		d.PortDetectTimeout.Seconds(), d.PortDetectTimeout.Seconds(),
		d.ServerURL,
		d.MQTTHost,
		d.MQTTPort,
		d.MQTTTopicPrefix,
	)
}
