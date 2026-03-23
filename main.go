package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	ui.Verbose = *verbose

	// -------------------------------------------------------------------------
	// Step 1 — Load config, write default template on first run, print banner
	// -------------------------------------------------------------------------
	cfgPath := ConfigPath()
	if err := LoadConfig(cfgPath); err != nil {
		// Non-fatal: fall back to defaults and warn.
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", cfgPath, err)
	}
	// Always ensure a default template exists so users can discover options.
	if err := WriteDefaultConfig(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write default config: %v\n", err)
	}

	if ui.Verbose {
		ui.Banner()
	}
	ui.Info("MeshMonitor %s — MeshCore network monitoring tool", Version)
	ui.Verb("Config file: %s", cfgPath)
	ui.Verb("Press Ctrl+C at any time to exit.")
	if ui.Verbose {
		fmt.Println()
	}

	// Check serial port permissions before doing anything else.
	checkDialoutAccess()

	// Trap Ctrl+C for a clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// -------------------------------------------------------------------------
	// Steps 2–5 — Serial port setup
	//   Skipped when serial_port is already set in the config file.
	// -------------------------------------------------------------------------
	var connectedPort string

	if cfg.SerialPort != "" {
		ui.Verb("Using configured serial port: %s", cfg.SerialPort)
		connectedPort = cfg.SerialPort
	} else {
		connectedPort = detectSerialPort(sigCh)
		if connectedPort == "" {
			return // user exited during detection
		}
		// Persist the detected port so future runs skip auto-detection.
		if err := SaveSerialPort(cfgPath, connectedPort); err != nil {
			ui.Warn("Could not save serial port to config: %v", err)
		} else {
			ui.Success("Serial port saved to %s", cfgPath)
		}
	}

	// Open device connection.
	device, err := NewDevice(connectedPort)
	if err != nil {
		ui.Error("Failed to open %s: %v", connectedPort, err)
		os.Exit(1)
	}
	defer device.Close()

	// -------------------------------------------------------------------------
	// Step 6 — Initial device handshake
	// -------------------------------------------------------------------------
	ui.Verb("Initialising device...")
	selfInfo, err := device.Init()
	if err != nil {
		ui.Error("Device init failed: %v", err)
		os.Exit(1)
	}
	if ui.Verbose {
		ui.PrintSelfInfo(selfInfo, device.DevInfo)
	}

	// -------------------------------------------------------------------------
	// First-run setup — server, MQTT, and token configuration
	// -------------------------------------------------------------------------
	if cfg.ServerToken == "" {
		fmt.Println()
		ui.Info("Public key: %s", selfInfo.PublicKeyHex)
		ui.Info("Use this key to register the device on the dashboard and obtain a token.")
		fmt.Println()

		// Server URL
		val, ok := promptWithDefault("Server URL", cfg.ServerURL, sigCh)
		if !ok {
			return
		}
		if val != cfg.ServerURL {
			cfg.ServerURL = val
			if err := SaveConfigValue(cfgPath, "server", "url", val); err != nil {
				ui.Warn("Could not save server URL: %v", err)
			}
		}

		// MQTT host
		val, ok = promptWithDefault("MQTT host", cfg.MQTTHost, sigCh)
		if !ok {
			return
		}
		if val != cfg.MQTTHost {
			cfg.MQTTHost = val
			if err := SaveConfigValue(cfgPath, "mqtt", "host", val); err != nil {
				ui.Warn("Could not save MQTT host: %v", err)
			}
		}

		// MQTT port
		val, ok = promptWithDefault("MQTT port", fmt.Sprintf("%d", cfg.MQTTPort), sigCh)
		if !ok {
			return
		}
		if p, pErr := fmt.Sscanf(val, "%d", &cfg.MQTTPort); pErr != nil || p != 1 {
			ui.Warn("Invalid port, using default %d", cfg.MQTTPort)
		} else {
			if val != fmt.Sprintf("%d", defaultConfig().MQTTPort) {
				if err := SaveConfigValue(cfgPath, "mqtt", "port", val); err != nil {
					ui.Warn("Could not save MQTT port: %v", err)
				}
			}
		}

		fmt.Println()

		// Token
		ui.Prompt("Enter server token (will be saved to config)")
		line, ok := readLineOrExit(sigCh)
		if !ok {
			return
		}
		cfg.ServerToken = strings.TrimSpace(line)
		if cfg.ServerToken == "" {
			ui.Error("No token provided — cannot authenticate with server.")
			os.Exit(1)
		}
		if err := SaveToken(cfgPath, cfg.ServerToken); err != nil {
			ui.Warn("Could not save token to config: %v", err)
		} else {
			ui.Success("Token saved to %s", cfgPath)
		}
	}

	if ui.Verbose {
		ui.Section("Registering with server")
	}
	if err := PostDeviceReport(selfInfo, device.DevInfo); err != nil {
		ui.Warn("Device report: %v", err)
	}

	// Fetch device config for MQTT credentials.
	devCfg, err := FetchDeviceConfig()
	if err != nil {
		ui.Warn("Could not fetch device config: %v", err)
	} else if devCfg.MQTT.Username != "" {
		mqttUsername = devCfg.MQTT.Username
		ui.Verb("MQTT username: %s", mqttUsername)
	}

	// -------------------------------------------------------------------------
	// Main monitoring loop
	//   Base cycle: 15 min — polls repeaters 0–1 hops away
	//   Every 2nd cycle (30 min) — also polls repeaters >1 hop away
	// -------------------------------------------------------------------------
	const (
		nearCycleInterval = 15 * time.Minute
		farCycleMultiple  = 2 // poll far repeaters every Nth cycle
		nearMaxHops       = 1
	)
	cycleNum := 0
	for {
		cycleNum++
		pollFar := cycleNum == 1 || cycleNum%farCycleMultiple == 0
		if ui.Verbose {
			label := "near"
			if pollFar {
				label = "near+far"
			}
			ui.Section(fmt.Sprintf("Monitoring Cycle %d (%s)", cycleNum, label))
		} else {
			ui.Info("Cycle %d", cycleNum)
		}

		// -------------------------------------------------------------------
		// Re-initialise device each cycle
		// -------------------------------------------------------------------
		ui.Verb("Initialising device...")
		selfInfo, err = device.Init()
		if err != nil {
			ui.Error("Device init failed: %v", err)
			ui.Warn("Retrying in 10 seconds...")
			if !sleepOrExit(10*time.Second, sigCh) {
				return
			}
			continue
		}

		// -------------------------------------------------------------------
		// Collect contacts; advertise and retry if none found
		// -------------------------------------------------------------------
		var contacts []*Contact
		var cached statusCache
		for attempt := 1; ; attempt++ {
			ui.Verb("Fetching contacts (attempt %d)...", attempt)
			contacts, err = device.GetContacts()
			if err != nil {
				ui.Warn("Could not fetch contacts: %v", err)
			}
			if len(contacts) > 0 {
				ui.Verb("Found %d contact(s).", len(contacts))

				// Discover paths for repeaters with unknown hop count.
				contacts, cached = discoverPaths(device, contacts, sigCh)

				if ui.Verbose {
					ui.PrintContacts(contacts)
				}
				if err := PostRepeaterContacts(contacts); err != nil {
					ui.Warn("Could not report contacts: %v", err)
				}
				break
			}
			ui.Warn("No contacts found. Sending advertisement and waiting %s...", cfg.AdvertWait)
			if sendErr := device.SendAdvert(); sendErr != nil {
				ui.Warn("Advert send failed: %v", sendErr)
			}
			if !ui.WaitWithSpinner("Waiting for contacts to appear", cfg.AdvertWait, sigCh) {
				ui.Info("Shutting down.")
				return
			}
		}

		// Build hop lookup from contacts.
		hopsByKey := make(map[string]int8, len(contacts))
		for _, c := range contacts {
			hopsByKey[c.PublicKeyHex] = c.PathLen
		}

		// -------------------------------------------------------------------
		// Fetch repeater list from server
		// -------------------------------------------------------------------
		if ui.Verbose {
			ui.Section("Fetching repeaters")
		}
		serverResp, err := FetchRepeaters()
		if err != nil {
			ui.Warn("Fetch repeaters: %v", err)
		}
		if serverResp == nil || len(serverResp.Repeaters) == 0 {
			ui.Warn("No repeaters to monitor this cycle.")
			if !sleepOrExit(nearCycleInterval, sigCh) {
				return
			}
			continue
		}
		if ui.Verbose {
			ui.PrintRepeaterTargets(serverResp.Repeaters)
		}

		// -------------------------------------------------------------------
		// Poll each repeater for status and telemetry
		// -------------------------------------------------------------------
		if ui.Verbose {
			ui.Section("Collecting repeater data")
		}
		for _, target := range serverResp.Repeaters {
			hops, known := hopsByKey[target.PublicKey]
			isNear := known && hops >= 0 && hops <= nearMaxHops

			if !isNear && !pollFar {
				ui.Verb("→ Skipping %s (%d hops, next far cycle)", target.Name, hops)
				continue
			}

			pubKey, decErr := hex.DecodeString(target.PublicKey)
			if decErr != nil || len(pubKey) != 32 {
				ui.Warn("Skipping %s — invalid public key: %v", target.Name, decErr)
				continue
			}

			hopLabel := "?"
			if known && hops >= 0 {
				hopLabel = fmt.Sprintf("%d", hops)
			}

			// Login if guest password is set for this repeater.
			needsLogout := false
			if target.GuestPassword != "" {
				ui.Verb("→ Login: %s (guest)", target.Name)
				if loginErr := device.Login(pubKey, target.GuestPassword); loginErr != nil {
					ui.Warn("  Login failed for %s: %v", target.Name, loginErr)
				} else {
					needsLogout = true
					ui.Verb("  Login success: %s", target.Name)
				}
				if !sleepOrExit(randomDelay(), sigCh) {
					return
				}
			}

			// Use cached status from path discovery if available.
			var status *StatusResponse
			var statusErr error
			if s, ok := cached[target.PublicKey]; ok {
				status = s
				ui.Verb("→ Status (cached): %s (%s hops)", target.Name, hopLabel)
			} else {
				ui.Verb("→ Status request: %s (%s hops)", target.Name, hopLabel)
				status, statusErr = device.RequestStatus(pubKey)
			}
			if statusErr != nil {
				ui.Warn("  No status response from %s: %v", target.Name, statusErr)
			} else if status != nil {
				if ui.Verbose {
					ui.PrintStatusResult(target, status)
				}
				if pubErr := PublishStatus(target, status); pubErr != nil {
					ui.Warn("  MQTT publish failed for %s: %v", target.Name, pubErr)
				}
			}

			if !sleepOrExit(randomDelay(), sigCh) {
				if needsLogout {
					device.Logout(pubKey)
				}
				return
			}

			// Telemetry request
			ui.Verb("→ Telemetry request: %s", target.Name)
			telem, telemErr := device.RequestTelemetry(pubKey)
			if telemErr != nil {
				ui.Warn("  No telemetry response from %s: %v", target.Name, telemErr)
			} else {
				if ui.Verbose {
					ui.PrintTelemetryResult(target, telem)
				}
				if pubErr := PublishTelemetry(target, telem); pubErr != nil {
					ui.Warn("  MQTT publish failed for %s: %v", target.Name, pubErr)
				}
			}

			// Logout if we logged in.
			if needsLogout {
				device.Logout(pubKey)
			}

			if !sleepOrExit(randomDelay(), sigCh) {
				return
			}
		}
		ui.Verb("Cycle %d complete.", cycleNum)

		// -------------------------------------------------------------------
		// Wait before the next cycle
		// -------------------------------------------------------------------
		if !ui.Countdown("Idle", nearCycleInterval, sigCh) {
			ui.Info("Shutting down.")
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Path discovery — probe repeaters with unknown hop count
// ---------------------------------------------------------------------------

// statusCache holds status responses obtained during path discovery,
// so we don't re-request repeaters that were already probed.
type statusCache map[string]*StatusResponse

const (
	discoveryFailFile    = "discovery_failures.json"
	discoveryCooldown    = 24 * time.Hour
	discoveryStaleWindow = 5 * time.Hour
)

// loadDiscoveryFailures reads the failure cooldown file.
func loadDiscoveryFailures() map[string]int64 {
	data, err := os.ReadFile(discoveryFailFile)
	if err != nil {
		return make(map[string]int64)
	}
	var failures map[string]int64
	if json.Unmarshal(data, &failures) != nil {
		return make(map[string]int64)
	}
	return failures
}

// saveDiscoveryFailures writes the failure cooldown file.
func saveDiscoveryFailures(failures map[string]int64) {
	data, err := json.Marshal(failures)
	if err != nil {
		return
	}
	_ = os.WriteFile(discoveryFailFile, data, 0644)
}

// discoverPaths sends a status request to each repeater contact with PathLen == -1
// to trigger mesh route discovery, then re-fetches the contact list to get updated
// hop counts. Returns the updated contact list and a cache of status responses
// that can be reused during the monitoring pass.
func discoverPaths(device *Device, contacts []*Contact, sigCh <-chan os.Signal) ([]*Contact, statusCache) {
	cache := make(statusCache)
	failures := loadDiscoveryFailures()
	now := time.Now()
	staleThreshold := now.Add(-discoveryStaleWindow).Unix()

	var unknown []*Contact
	for _, c := range contacts {
		if c.Type != AdvTypeRepeater || c.PathLen >= 0 {
			continue
		}
		if c.LastAdvert <= uint32(staleThreshold) {
			continue
		}
		if failTime, ok := failures[c.PublicKeyHex]; ok && now.Unix()-failTime < int64(discoveryCooldown.Seconds()) {
			ui.Verb("  Skipping %s — failed discovery less than 24h ago", c.Name)
			continue
		}
		unknown = append(unknown, c)
	}
	if len(unknown) == 0 {
		return contacts, cache
	}

	ui.Verb("Discovering paths for %d repeater(s) with unknown hops...", len(unknown))
	for _, c := range unknown {
		select {
		case <-sigCh:
			ui.Info("Shutting down.")
			return contacts, cache
		default:
		}

		ui.Verb("  Path discovery: %s...", c.Name)

		ch := make(chan error, 1)
		go func() {
			ch <- device.PathDiscovery(c.PublicKey)
		}()

		select {
		case <-ch:
		case <-sigCh:
			ui.Info("Shutting down.")
			return contacts, cache
		}

		if !sleepOrExit(1*time.Second, sigCh) {
			return contacts, cache
		}
	}

	// Re-fetch contacts to pick up updated path info.
	updated, err := device.GetContacts()
	if err != nil || len(updated) == 0 {
		ui.Verb("  Could not re-fetch contacts after path discovery")
		updated = contacts
	}

	// Check which probed repeaters resolved vs still unknown.
	resolvedSet := make(map[string]bool)
	for _, c := range updated {
		if c.Type == AdvTypeRepeater && c.PathLen >= 0 {
			for _, u := range unknown {
				if c.PublicKeyHex == u.PublicKeyHex {
					resolvedSet[c.PublicKeyHex] = true
					break
				}
			}
		}
	}
	failedKeys := make(map[string]bool)
	for _, u := range unknown {
		if !resolvedSet[u.PublicKeyHex] {
			failedKeys[u.PublicKeyHex] = true
		}
	}

	// Update failure file.
	if len(failedKeys) > 0 {
		for key := range failedKeys {
			failures[key] = now.Unix()
		}
		// Prune entries older than cooldown.
		for key, ts := range failures {
			if now.Unix()-ts >= int64(discoveryCooldown.Seconds()) {
				delete(failures, key)
			}
		}
		saveDiscoveryFailures(failures)
	}

	resolved := len(resolvedSet)
	ui.Verb("  Resolved %d of %d unknown paths", resolved, len(unknown))
	return updated, cache
}

// ---------------------------------------------------------------------------
// Serial port detection wizard (steps 2–5)
// ---------------------------------------------------------------------------

func detectSerialPort(sigCh <-chan os.Signal) string {
	ui.Info("Connect (or reconnect) your MeshCore device to USB...")
	ui.Info("Waiting for device (up to %s)...", cfg.PortDetectTimeout)

	port, err := DetectDevice(cfg.PortDetectTimeout, sigCh)
	if err != nil {
		if err.Error() == "interrupted" {
			ui.Info("Shutting down.")
			return ""
		}
		ui.Error("Device not detected: %v", err)
		os.Exit(1)
	}
	ui.Success("Device detected on port: %s", port)
	return port
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stdinLines returns a channel that emits one value per line read from stdin.
// Runs in a background goroutine for the lifetime of the process.
var stdinLines = func() <-chan string {
	ch := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			ch <- scanner.Text()
		}
		close(ch)
	}()
	return ch
}()

// promptWithDefault shows a prompt with a default value in brackets.
// Pressing Enter without input accepts the default.
func promptWithDefault(label, defaultVal string, sigCh <-chan os.Signal) (string, bool) {
	fmt.Printf("%s%s?%s %s [%s]: ", ansiBold, ansiYellow, ansiReset, label, defaultVal)
	line, ok := readLineOrExit(sigCh)
	if !ok {
		return "", false
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal, true
	}
	return line, true
}

func readLineOrExit(sigCh <-chan os.Signal) (string, bool) {
	select {
	case line, ok := <-stdinLines:
		if !ok {
			return "", false
		}
		return line, true
	case <-sigCh:
		ui.Info("Shutting down.")
		return "", false
	}
}

func sleepOrExit(d time.Duration, sigCh <-chan os.Signal) bool {
	select {
	case <-time.After(d):
		return true
	case <-sigCh:
		ui.Info("Shutting down.")
		return false
	}
}

func randomDelay() time.Duration {
	spread := cfg.MaxDelayBetweenReqs - cfg.MinDelayBetweenReqs
	return cfg.MinDelayBetweenReqs + time.Duration(rand.Int63n(int64(spread)))
}
