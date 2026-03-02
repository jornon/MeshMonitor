package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
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

	ui.Banner()
	ui.Info("MeshMonitor — MeshCore network monitoring tool")
	ui.Info("Config file: %s", cfgPath)
	ui.Info("Press Ctrl+C at any time to exit.")
	fmt.Println()

	// Trap Ctrl+C for a clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// -------------------------------------------------------------------------
	// Steps 2–5 — Serial port setup
	//   Skipped when serial_port is already set in the config file.
	// -------------------------------------------------------------------------
	var connectedPort string

	if cfg.SerialPort != "" {
		ui.Info("Using configured serial port: %s", cfg.SerialPort)
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
	// Main monitoring loop
	// -------------------------------------------------------------------------
	cycleNum := 0
	for {
		cycleNum++
		ui.Section(fmt.Sprintf("Monitoring Cycle %d", cycleNum))

		// -------------------------------------------------------------------
		// Step 6 — Collect basic device info
		// -------------------------------------------------------------------
		ui.Info("Initialising device...")
		selfInfo, err := device.Init()
		if err != nil {
			ui.Error("Device init failed: %v", err)
			ui.Warn("Retrying in 10 seconds...")
			if !sleepOrExit(10*time.Second, sigCh) {
				return
			}
			continue
		}
		ui.PrintSelfInfo(selfInfo, device.DevInfo)

		// -------------------------------------------------------------------
		// Step 7 — Collect contacts; advertise and retry if none found
		// -------------------------------------------------------------------
		var contacts []*Contact
		for attempt := 1; ; attempt++ {
			ui.Info("Fetching contacts (attempt %d)...", attempt)
			contacts, err = device.GetContacts()
			if err != nil {
				ui.Warn("Could not fetch contacts: %v", err)
			}
			if len(contacts) > 0 {
				ui.Success("Found %d contact(s).", len(contacts))
				ui.PrintContacts(contacts)
				break
			}
			ui.Warn("No contacts found. Sending advertisement and waiting %s...", cfg.AdvertWait)
			if sendErr := device.SendAdvert(); sendErr != nil {
				ui.Warn("Advert send failed: %v", sendErr)
			}
			ui.WaitWithSpinner("Waiting for contacts to appear", cfg.AdvertWait)
			select {
			case <-sigCh:
				ui.Info("Shutting down.")
				return
			default:
			}
		}

		// -------------------------------------------------------------------
		// Step 8 — POST to server; receive list of repeaters to poll
		// -------------------------------------------------------------------
		ui.Section("Reporting to server")
		serverResp, err := PostDeviceReport(selfInfo, contacts)
		if err != nil {
			ui.Warn("Server: %v", err)
		}
		if serverResp == nil || len(serverResp.Repeaters) == 0 {
			ui.Warn("No repeaters to monitor this cycle.")
			if !sleepOrExit(cfg.CycleInterval, sigCh) {
				return
			}
			continue
		}
		ui.PrintRepeaterTargets(serverResp.Repeaters)

		// -------------------------------------------------------------------
		// Steps 9–11 — Poll each repeater for status and telemetry
		// -------------------------------------------------------------------
		ui.Section("Collecting repeater data")
		for _, target := range serverResp.Repeaters {
			pubKey, decErr := hex.DecodeString(target.PublicKey)
			if decErr != nil || len(pubKey) != 32 {
				ui.Warn("Skipping %s — invalid public key: %v", target.Name, decErr)
				continue
			}

			// Status request
			ui.Info("→ Status request: %s", target.Name)
			status, statusErr := device.RequestStatus(pubKey)
			if statusErr != nil {
				ui.Warn("  No status response from %s: %v", target.Name, statusErr)
			} else {
				ui.PrintStatusResult(target, status)
				if pubErr := PublishStatus(target, status); pubErr != nil {
					ui.Warn("  MQTT publish failed: %v", pubErr)
				}
			}

			if !sleepOrExit(randomDelay(), sigCh) {
				return
			}

			// Telemetry request
			ui.Info("→ Telemetry request: %s", target.Name)
			telem, telemErr := device.RequestTelemetry(pubKey)
			if telemErr != nil {
				ui.Warn("  No telemetry response from %s: %v", target.Name, telemErr)
			} else {
				ui.PrintTelemetryResult(target, telem)
				if pubErr := PublishTelemetry(target, telem); pubErr != nil {
					ui.Warn("  MQTT publish failed: %v", pubErr)
				}
			}

			if !sleepOrExit(randomDelay(), sigCh) {
				return
			}
		}
		ui.Success("Cycle %d complete.", cycleNum)

		// -------------------------------------------------------------------
		// Step 12 — Wait before the next cycle
		// -------------------------------------------------------------------
		ui.Countdown("Idle", cfg.CycleInterval)
		select {
		case <-sigCh:
			ui.Info("Shutting down.")
			return
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// Serial port detection wizard (steps 2–5)
// ---------------------------------------------------------------------------

func detectSerialPort(sigCh <-chan os.Signal) string {
	// Step 2 — Ask the user to disconnect the device first.
	ui.Prompt("If your MeshCore device is already connected, disconnect it now, then press Enter")
	waitForEnter()

	// Step 3 — Snapshot current ports.
	ui.Info("Scanning serial ports...")
	beforePorts, err := ListPorts()
	if err != nil {
		ui.Error("Could not list serial ports: %v", err)
		os.Exit(1)
	}
	if len(beforePorts) > 0 {
		ui.Info("Found %d existing port(s):", len(beforePorts))
		ui.PrintPorts(beforePorts)
	} else {
		ui.Info("No serial ports currently detected.")
	}
	fmt.Println()

	// Step 4 — Ask the user to connect the device.
	ui.Prompt("Connect your MeshCore device to USB, then press Enter")
	waitForEnter()

	// Step 5 — Detect the newly appeared port.
	ui.Info("Waiting for new serial port (up to %s)...", cfg.PortDetectTimeout)
	newPort, err := DetectNewPort(beforePorts, cfg.PortDetectTimeout)
	if err != nil {
		ui.Error("Device not detected: %v", err)
		os.Exit(1)
	}
	ui.Success("Device detected on port: %s", newPort)
	return newPort
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func waitForEnter() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
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
