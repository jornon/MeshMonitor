package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	verbose := flag.Bool("v", false, "verbose output")
	checkUpdate := flag.Bool("check-update", false, "check for updates and exit")
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

	// Handle --check-update: print result and exit immediately.
	if *checkUpdate {
		handleCheckUpdateFlag()
		return
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
	// Auto-update — check at startup, then periodically in the background
	// -------------------------------------------------------------------------
	updateCh := make(chan bool, 1)
	if cfg.AutoUpdate {
		ui.Verb("Checking for updates...")
		if performUpdate() {
			return // unreachable after exec, but keeps the compiler happy
		}
		// Start background update checker.
		go runUpdateLoop(updateCh)
	}

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

		// Query and publish companion node battery (#6).
		if battMV, battErr := device.GetBattAndStorage(); battErr == nil {
			ui.Verb("Companion battery: %dmV", battMV)
			if pubErr := PublishCompanionStats(battMV, selfInfo); pubErr != nil {
				ui.Dimf("[mqtt] companion stats publish failed: %v\n", pubErr)
			}
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

				// Reset stale paths so they get re-discovered.
				contacts = refreshStalePaths(device, contacts, sigCh)

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

		// Build hop, GPS, and prefix lookups from contacts.
		hopsByKey := make(map[string]int8, len(contacts))
		gpsByKey := make(map[string]*[2]float64, len(contacts))
		contactsByPrefix := make(map[string]string, len(contacts)) // 4-byte hex prefix → full key
		for _, c := range contacts {
			hopsByKey[c.PublicKeyHex] = c.PathLen
			if len(c.PublicKeyHex) >= 8 {
				contactsByPrefix[c.PublicKeyHex[:8]] = c.PublicKeyHex
			}
			if c.Lat != 0 || c.Lon != 0 {
				gps := [2]float64{c.Lat, c.Lon}
				gpsByKey[c.PublicKeyHex] = &gps
			}
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
		//
		// Requests are spread evenly across the cycle interval to minimise
		// concentrated traffic on the mesh network.
		// -------------------------------------------------------------------
		if ui.Verbose {
			ui.Section("Collecting repeater data")
		}

		// Count how many repeaters will actually be polled this cycle.
		var pollTargets []RepeaterTarget
		for _, target := range serverResp.Repeaters {
			hops, known := hopsByKey[target.PublicKey]
			isNear := known && hops >= 0 && hops <= nearMaxHops
			isUnknown := !known || hops < 0
			if !isNear && !isUnknown && !pollFar {
				ui.RepeaterSkip(target.Name, fmt.Sprintf("%d hops, next far cycle", hops))
				continue
			}
			pollTargets = append(pollTargets, target)
		}

		// Calculate inter-repeater delay to spread requests across the cycle.
		interDelay := time.Duration(0)
		if len(pollTargets) > 1 {
			interDelay = nearCycleInterval / time.Duration(len(pollTargets))
			// Cap so we don't wait absurdly long per repeater.
			if interDelay > 5*time.Minute {
				interDelay = 5 * time.Minute
			}
		}
		if len(pollTargets) > 0 {
			ui.Verb("Polling %d repeater(s), interval %s between each", len(pollTargets), interDelay.Truncate(time.Second))
		}

		for i, target := range pollTargets {
			pubKey, decErr := hex.DecodeString(target.PublicKey)
			if decErr != nil || len(pubKey) != 32 {
				ui.Warn("Skipping %s — invalid public key: %v", target.Name, decErr)
				continue
			}

			hops := hopsByKey[target.PublicKey]
			hopLabel := "?"
			if _, known := hopsByKey[target.PublicKey]; known && hops >= 0 {
				hopLabel = fmt.Sprintf("%d", hops)
			}

			// ── Repeater header ──
			ui.RepeaterHeader(i+1, len(pollTargets), target.Name, hopLabel, target.GuestPassword != "")

			// Login if guest password is set for this repeater.
			needsLogout := false
			if target.GuestPassword != "" {
				if loginErr := device.Login(pubKey, target.GuestPassword); loginErr != nil {
					ui.RepeaterLoginFail(loginErr)
				} else {
					needsLogout = true
					ui.RepeaterLoginOK()
				}
			}

			// ── Status ──
			var status *StatusResponse
			var statusErr error
			if s, ok := cached[target.PublicKey]; ok {
				status = s
			} else {
				status, statusErr = device.RequestStatus(pubKey)
			}
			if statusErr != nil {
				ui.RepeaterFail("Status", statusErr)
			} else if status != nil {
				ui.RepeaterStatus(target.Name, status)
				if pubErr := PublishStatus(target, status, gpsByKey[target.PublicKey]); pubErr != nil {
					ui.RepeaterFail("MQTT status", pubErr)
				}
			}

			// ── Telemetry ──
			if target.CollectTemperature {
				time.Sleep(InterRequestDelay)
				telem, telemErr := device.RequestTelemetry(pubKey)
				if telemErr != nil {
					ui.RepeaterFail("Telemetry", telemErr)
				} else {
					ui.RepeaterTelemetry(target.Name, telem)
					if pubErr := PublishTelemetry(target, telem, gpsByKey[target.PublicKey]); pubErr != nil {
						ui.RepeaterFail("MQTT telemetry", pubErr)
					}
				}
			}

			// ── Neighbours ──
			if pollFar {
				time.Sleep(InterRequestDelay)
				neighbours, nErr := device.RequestNeighbours(pubKey)
				if nErr != nil {
					ui.Dimf("     🗺️  No neighbours: %v\n", nErr)
				} else if len(neighbours) > 0 {
					ui.RepeaterNeighbours(target.Name, neighbours)
					if pubErr := PublishNeighbours(target, neighbours, contactsByPrefix); pubErr != nil {
						ui.RepeaterFail("MQTT neighbours", pubErr)
					}
				}
			}

			// Logout if we logged in.
			if needsLogout {
				device.Logout(pubKey)
			}

			// Wait before the next repeater (skip after the last one).
			if i < len(pollTargets)-1 && interDelay > 0 {
				if !ui.Countdown("Next request", interDelay, sigCh) {
					ui.Info("Shutting down.")
					return
				}
			}
		}
		ui.Verb("Cycle %d complete.", cycleNum)

		// -------------------------------------------------------------------
		// Wait before the next cycle — apply pending updates if available
		// -------------------------------------------------------------------
		select {
		case <-updateCh:
			ui.Info("Update available — applying now...")
			device.Close()
			performUpdate()
			// If we reach here, update failed — reopen device.
			ui.Warn("Update failed, continuing normal operation.")
			device, err = NewDevice(connectedPort)
			if err != nil {
				ui.Error("Failed to reopen device after update: %v", err)
				os.Exit(1)
			}
		default:
		}
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

	// pathMaxAge is the maximum age of a cached path before it is reset.
	// If a repeater's path hasn't been updated in this window, we force
	// re-discovery to ensure the route is still valid.
	pathMaxAge = 6 * time.Hour

	// pathAdvertDrift is the minimum gap between LastAdvert and LastMod
	// that triggers a path reset. If the contact re-advertised (e.g. after
	// moving or rebooting) but the path wasn't refreshed, the route may
	// be stale.
	pathAdvertDrift = 30 * time.Minute
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
// Stale path refresh — reset paths that are likely outdated
// ---------------------------------------------------------------------------

// refreshStalePaths identifies repeaters with cached paths that may be outdated
// and resets them via CMD_RESET_PATH. A path is considered stale when:
//   - It is older than pathMaxAge, OR
//   - The contact re-advertised (LastAdvert) significantly after the path was
//     last modified (LastMod), suggesting the node moved or rebooted.
//
// After resetting, a re-fetch of contacts picks up the cleared paths (PathLen = -1),
// which the normal discoverPaths flow will then re-probe.
func refreshStalePaths(device *Device, contacts []*Contact, sigCh <-chan os.Signal) []*Contact {
	now := uint32(time.Now().Unix())
	maxAgeThreshold := now - uint32(pathMaxAge.Seconds())
	driftThreshold := uint32(pathAdvertDrift.Seconds())

	var stale []*Contact
	for _, c := range contacts {
		if c.Type != AdvTypeRepeater || c.PathLen < 0 {
			continue // skip non-repeaters and already-unknown paths
		}
		// Check 1: path is older than max age.
		if c.LastMod > 0 && c.LastMod < maxAgeThreshold {
			stale = append(stale, c)
			continue
		}
		// Check 2: contact re-advertised but path wasn't refreshed.
		// LastAdvert is the remote node's clock, LastMod is our local clock.
		// We compare the gap: if advert is much newer than lastmod, the node
		// has been heard recently but via a potentially different route.
		if c.LastAdvert > 0 && c.LastMod > 0 && c.LastAdvert > c.LastMod+driftThreshold {
			stale = append(stale, c)
			continue
		}
	}

	if len(stale) == 0 {
		return contacts
	}

	ui.Verb("Resetting %d stale path(s)...", len(stale))
	for _, c := range stale {
		select {
		case <-sigCh:
			return contacts
		default:
		}
		ui.Verb("  Reset path: %s (hops=%d, lastmod=%d, advert=%d)", c.Name, c.PathLen, c.LastMod, c.LastAdvert)
		if err := device.ResetPath(c.PublicKey); err != nil {
			ui.Warn("  Reset path failed for %s: %v", c.Name, err)
		}
		if !sleepOrExit(500*time.Millisecond, sigCh) {
			return contacts
		}
	}

	// Re-fetch contacts — the reset paths will now show PathLen = -1.
	updated, err := device.GetContacts()
	if err != nil || len(updated) == 0 {
		ui.Verb("  Could not re-fetch contacts after path reset")
		return contacts
	}
	return updated
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

