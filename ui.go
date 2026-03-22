package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// UI provides simple ANSI-coloured terminal output helpers.
// All output goes to stdout.
type UI struct {
	Verbose bool
}

var ui = &UI{}

// ANSI colour codes
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiRed    = "\033[31m"
	ansiWhite  = "\033[37m"
)

func (u *UI) Banner() {
	fmt.Println()
	fmt.Printf("%s%s╔══════════════════════════════════════╗%s\n", ansiBold, ansiCyan, ansiReset)
	fmt.Printf("%s%s║          M E S H M O N I T O R       ║%s\n", ansiBold, ansiCyan, ansiReset)
	fmt.Printf("%s%s╚══════════════════════════════════════╝%s\n", ansiBold, ansiCyan, ansiReset)
	fmt.Println()
}

func (u *UI) Info(format string, args ...any) {
	fmt.Printf("%s[i]%s %s\n", ansiCyan, ansiReset, fmt.Sprintf(format, args...))
}

func (u *UI) Success(format string, args ...any) {
	fmt.Printf("%s[✓]%s %s\n", ansiGreen, ansiReset, fmt.Sprintf(format, args...))
}

func (u *UI) Warn(format string, args ...any) {
	fmt.Printf("%s[!]%s %s\n", ansiYellow, ansiReset, fmt.Sprintf(format, args...))
}

func (u *UI) Error(format string, args ...any) {
	fmt.Printf("%s[✗]%s %s\n", ansiRed, ansiReset, fmt.Sprintf(format, args...))
}

func (u *UI) Dimf(format string, args ...any) {
	if !u.Verbose {
		return
	}
	fmt.Printf("%s%s%s", ansiDim, fmt.Sprintf(format, args...), ansiReset)
}

// Verb prints info-level output only in verbose mode.
func (u *UI) Verb(format string, args ...any) {
	if !u.Verbose {
		return
	}
	fmt.Printf("%s[i]%s %s\n", ansiCyan, ansiReset, fmt.Sprintf(format, args...))
}

func (u *UI) Section(title string) {
	fmt.Println()
	fmt.Printf("%s%s── %s %s%s\n", ansiBold, ansiWhite, title, strings.Repeat("─", max(0, 40-len(title))), ansiReset)
}

func (u *UI) Prompt(msg string) {
	fmt.Printf("%s%s?%s %s: ", ansiBold, ansiYellow, ansiReset, msg)
}

// PrintPorts displays the list of serial ports in a simple table.
func (u *UI) PrintPorts(ports []string) {
	if len(ports) == 0 {
		u.Warn("No serial ports detected.")
		return
	}
	fmt.Printf("%s  %-4s  %s%s\n", ansiDim, "#", "Port", ansiReset)
	for i, p := range ports {
		fmt.Printf("  %-4d  %s\n", i+1, p)
	}
}

// PrintSelfInfo displays device identity information.
func (u *UI) PrintSelfInfo(info *SelfInfo, devInfo *DeviceInfo) {
	u.Section("Device Info")
	fmt.Printf("  Name:       %s%s%s\n", ansiBold, info.Name, ansiReset)
	fmt.Printf("  Type:       %s\n", AdvTypeNames[info.AdvType])
	fmt.Printf("  Public key: %s...%s\n", info.PublicKeyHex[:12], info.PublicKeyHex[len(info.PublicKeyHex)-8:])
	if info.Lat != 0 || info.Lon != 0 {
		fmt.Printf("  Location:   %.6f, %.6f\n", info.Lat, info.Lon)
	}
	fmt.Printf("  Radio:      %.3f MHz  BW=%.0f kHz  SF%d  CR4/%d\n",
		float64(info.RadioFreqKHz)/1000.0,
		float64(info.RadioBWKHz),
		info.RadioSF, info.RadioCR)
	if devInfo != nil {
		fmt.Printf("  Firmware:   %s (%s)\n", devInfo.Version, devInfo.FirmwareBuild)
		fmt.Printf("  Model:      %s\n", devInfo.Model)
	}
}

// PrintContacts displays repeaters from the contact list in a table.
func (u *UI) PrintContacts(contacts []*Contact) {
	var repeaters []*Contact
	for _, c := range contacts {
		if c.Type == AdvTypeRepeater {
			repeaters = append(repeaters, c)
		}
	}
	u.Section(fmt.Sprintf("Repeaters (%d of %d contacts)", len(repeaters), len(contacts)))
	if len(repeaters) == 0 {
		u.Warn("No repeaters found.")
		return
	}
	fmt.Printf("%s  %-20s  %-14s  %s%s\n",
		ansiDim, "Name", "Public Key", "Location", ansiReset)
	fmt.Printf("%s  %s%s\n", ansiDim, strings.Repeat("─", 52), ansiReset)
	for _, c := range repeaters {
		loc := ""
		if c.Lat != 0 || c.Lon != 0 {
			loc = fmt.Sprintf("%.4f,%.4f", c.Lat, c.Lon)
		}
		name := c.Name
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		fmt.Printf("  %-20s  %s...  %s\n",
			name, c.PublicKeyHex[:12], loc)
	}
}

// PrintRepeaterTargets displays the repeaters returned by the server.
func (u *UI) PrintRepeaterTargets(targets []RepeaterTarget) {
	u.Section(fmt.Sprintf("Repeaters to monitor (%d)", len(targets)))
	for i, t := range targets {
		fmt.Printf("  %d. %s%s%s  (%s...)\n", i+1, ansiBold, t.Name, ansiReset, t.PublicKey[:12])
	}
}

// PrintStatusResult displays a status response inline.
func (u *UI) PrintStatusResult(target RepeaterTarget, s *StatusResponse) {
	fmt.Printf("  %s%s%s  batt=%.2fV  RSSI=%ddBm  SNR=%.1fdB  up=%s  pkts_recv=%d\n",
		ansiGreen, target.Name, ansiReset,
		float64(s.BattMilliVolts)/1000.0,
		s.LastRSSI,
		float64(s.LastSNRx4)/4.0,
		formatDuration(time.Duration(s.UpTimeSecs)*time.Second),
		s.PacketsRecv,
	)
}

// PrintTelemetryResult displays a telemetry response inline.
func (u *UI) PrintTelemetryResult(target RepeaterTarget, t *TelemetryResponse) {
	fmt.Printf("  %s%s%s  telemetry=%d bytes (%s...)\n",
		ansiGreen, target.Name, ansiReset,
		len(t.RawData),
		func() string {
			if len(t.RawHex) > 16 {
				return t.RawHex[:16]
			}
			return t.RawHex
		}(),
	)
}

// Countdown shows a live countdown in-place. Returns false if interrupted by signal.
func (u *UI) Countdown(label string, d time.Duration, sigCh <-chan os.Signal) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		remaining := time.Until(end).Truncate(time.Second)
		fmt.Printf("\r%s[i]%s %s — next cycle in %s   ",
			ansiCyan, ansiReset, label, remaining)
		select {
		case <-sigCh:
			fmt.Printf("\r%s\r", strings.Repeat(" ", 70))
			return false
		case <-time.After(time.Second):
		}
	}
	fmt.Printf("\r%s\r", strings.Repeat(" ", 70)) // clear line
	return true
}

// WaitWithSpinner shows a spinning indicator while waiting for d. Returns false if interrupted.
func (u *UI) WaitWithSpinner(label string, d time.Duration, sigCh <-chan os.Signal) bool {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	end := time.Now().Add(d)
	i := 0
	for time.Now().Before(end) {
		remaining := time.Until(end).Truncate(time.Second)
		fmt.Printf("\r%s%s%s %s (%s)   ",
			ansiCyan, frames[i%len(frames)], ansiReset, label, remaining)
		select {
		case <-sigCh:
			fmt.Printf("\r%s\r", strings.Repeat(" ", 70))
			return false
		case <-time.After(100 * time.Millisecond):
		}
		i++
	}
	fmt.Printf("\r%s\r", strings.Repeat(" ", 70))
	return true
}

func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
