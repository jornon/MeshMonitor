package main

import (
	"fmt"
	"os"
	"os/user"
	"time"

	"go.bug.st/serial"
)

// checkDialoutAccess verifies that the current user belongs to the "dialout"
// group (required for serial port access on Linux). Exits with an actionable
// error message if not.
func checkDialoutAccess() {
	// Root can always access serial ports.
	if os.Getuid() == 0 {
		return
	}

	u, err := user.Current()
	if err != nil {
		return // can't determine — let it fail later with the real error
	}

	groups, err := u.GroupIds()
	if err != nil {
		return
	}

	dialout, err := user.LookupGroup("dialout")
	if err != nil {
		return // no dialout group on this system — skip check
	}

	for _, gid := range groups {
		if gid == dialout.Gid {
			return
		}
	}

	ui.Error("User %q is not in the \"dialout\" group — serial port access will be denied.", u.Username)
	ui.Info("Fix with:  sudo usermod -aG dialout %s", u.Username)
	ui.Info("Then log out and back in (or run: newgrp dialout)")
	os.Exit(1)
}

// ListPorts returns the current list of serial port names on the system.
func ListPorts() ([]string, error) {
	return serial.GetPortsList()
}

// DetectDevice polls for a serial port change (new port appearing, or a port
// disappearing and reappearing) and returns the port name. This handles both
// first-time connections and reconnections of an already-known device.
// Respects Ctrl+C via sigCh.
func DetectDevice(timeout time.Duration, sigCh <-chan os.Signal) (string, error) {
	initial, err := ListPorts()
	if err != nil {
		return "", fmt.Errorf("list ports: %w", err)
	}
	before := make(map[string]struct{}, len(initial))
	for _, p := range initial {
		before[p] = struct{}{}
	}

	// Track ports that have disappeared — if they come back, that's a reconnect.
	disappeared := make(map[string]struct{})

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-sigCh:
			return "", fmt.Errorf("interrupted")
		default:
		}

		current, err := ListPorts()
		if err != nil {
			return "", fmt.Errorf("list ports: %w", err)
		}
		currentSet := make(map[string]struct{}, len(current))
		for _, p := range current {
			currentSet[p] = struct{}{}
		}

		// Check for ports that disappeared since last poll.
		for _, p := range initial {
			if _, still := currentSet[p]; !still {
				disappeared[p] = struct{}{}
			}
		}
		// Also remove from initial so we track the latest baseline.
		var remaining []string
		for _, p := range initial {
			if _, gone := disappeared[p]; !gone {
				remaining = append(remaining, p)
			}
		}
		initial = remaining

		// Check for new or reconnected ports.
		for _, p := range current {
			if _, wasOriginal := before[p]; !wasOriginal {
				// Completely new port.
				return p, nil
			}
			if _, wasGone := disappeared[p]; wasGone {
				// Port disappeared and came back — reconnect.
				return p, nil
			}
		}

		select {
		case <-sigCh:
			return "", fmt.Errorf("interrupted")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("no device detected within %s", timeout)
}


