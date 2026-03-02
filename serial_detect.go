package main

import (
	"fmt"
	"time"

	"go.bug.st/serial"
)

// ListPorts returns the current list of serial port names on the system.
func ListPorts() ([]string, error) {
	return serial.GetPortsList()
}

// DetectNewPort polls until a new serial port appears that was not in beforePorts.
// Returns the new port name, or an error if the timeout is reached.
func DetectNewPort(beforePorts []string, timeout time.Duration) (string, error) {
	before := make(map[string]struct{}, len(beforePorts))
	for _, p := range beforePorts {
		before[p] = struct{}{}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, err := ListPorts()
		if err != nil {
			return "", fmt.Errorf("list ports: %w", err)
		}
		for _, p := range current {
			if _, seen := before[p]; !seen {
				return p, nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("no new serial port detected within %s", timeout)
}
