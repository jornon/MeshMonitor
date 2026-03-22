package main

import (
	"bufio"
	"encoding/hex"
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
	// -------------------------------------------------------------------------
	cycleNum := 0
	for {
		cycleNum++
		if ui.Verbose {
			ui.Section(fmt.Sprintf("Monitoring Cycle %d", cycleNum))
		} else {
			ui.Info("Cycle %d", cycleNum)
		}

		// -------------------------------------------------------------------
		// Step 7 — Re-initialise device each cycle (refreshes self info,
		//           syncs clock, discards stale buffers)
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
		// Step 7 — Collect contacts; advertise and retry if none found
		// -------------------------------------------------------------------
		var contacts []*Contact
		for attempt := 1; ; attempt++ {
			ui.Verb("Fetching contacts (attempt %d)...", attempt)
			contacts, err = device.GetContacts()
			if err != nil {
				ui.Warn("Could not fetch contacts: %v", err)
			}
			if len(contacts) > 0 {
				ui.Verb("Found %d contact(s).", len(contacts))
				if ui.Verbose {
					ui.PrintContacts(contacts)
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

		// -------------------------------------------------------------------
		// Step 9 — Fetch repeater list from server
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
			if !sleepOrExit(cfg.CycleInterval, sigCh) {
				return
			}
			continue
		}
		if ui.Verbose {
			ui.PrintRepeaterTargets(serverResp.Repeaters)
		}

		// -------------------------------------------------------------------
		// Steps 9–11 — Poll each repeater for status and telemetry
		// -------------------------------------------------------------------
		if ui.Verbose {
			ui.Section("Collecting repeater data")
		}
		for _, target := range serverResp.Repeaters {
			pubKey, decErr := hex.DecodeString(target.PublicKey)
			if decErr != nil || len(pubKey) != 32 {
				ui.Warn("Skipping %s — invalid public key: %v", target.Name, decErr)
				continue
			}

			// Status request
			ui.Verb("→ Status request: %s", target.Name)
			status, statusErr := device.RequestStatus(pubKey)
			if statusErr != nil {
				ui.Warn("  No status response from %s: %v", target.Name, statusErr)
			} else {
				if ui.Verbose {
					ui.PrintStatusResult(target, status)
				}
				if pubErr := PublishStatus(target, status); pubErr != nil {
					ui.Warn("  MQTT publish failed for %s: %v", target.Name, pubErr)
				}
			}

			if !sleepOrExit(randomDelay(), sigCh) {
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

			if !sleepOrExit(randomDelay(), sigCh) {
				return
			}
		}
		ui.Verb("Cycle %d complete.", cycleNum)

		// -------------------------------------------------------------------
		// Step 12 — Wait before the next cycle
		// -------------------------------------------------------------------
		if !ui.Countdown("Idle", cfg.CycleInterval, sigCh) {
			ui.Info("Shutting down.")
			return
		}
	}
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
