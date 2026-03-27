package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	githubReleasesURL = "https://api.github.com/repos/jornon/MeshMonitor/releases/latest"
	updateHTTPTimeout = 30 * time.Second
	downloadTimeout   = 5 * time.Minute
)

// githubRelease maps the subset of the GitHub Releases API response we need.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	Size               int    `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ---------------------------------------------------------------------------
// Version comparison
// ---------------------------------------------------------------------------

// isNewerVersion returns true if latest (e.g. "v0.2.0") is newer than current.
// Current may be "dev" or a dirty build like "v0.1.0-5-gad8ba88".
func isNewerVersion(current, latest string) bool {
	if current == "dev" || current == "" {
		return true
	}
	cur := parseSemver(current)
	lat := parseSemver(latest)
	if cur == nil || lat == nil {
		return false
	}
	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	if lat[2] != cur[2] {
		return lat[2] > cur[2]
	}
	// Same semver — upgrade if current is a dirty build (has dash suffix).
	stripped := strings.TrimPrefix(current, "v")
	return strings.Contains(stripped, "-")
}

// parseSemver extracts [major, minor, patch] from strings like "v0.1.0" or "v0.1.0-5-gabcdef".
func parseSemver(s string) []int {
	s = strings.TrimPrefix(s, "v")
	// Strip everything after the first dash (pre-release/dirty suffix).
	if i := strings.Index(s, "-"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil
	}
	ver := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		ver[i] = n
	}
	return ver
}

// ---------------------------------------------------------------------------
// Asset matching
// ---------------------------------------------------------------------------

// assetName returns the expected binary name for this platform.
func assetName() string {
	os := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv6"
	}
	name := fmt.Sprintf("meshmonitor-%s-%s", os, arch)
	if os == "windows" {
		name += ".exe"
	}
	return name
}

// findAsset returns the download URL for the matching binary in a release.
func findAsset(release *githubRelease) (string, int, error) {
	want := assetName()
	for _, a := range release.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL, a.Size, nil
		}
	}
	return "", 0, fmt.Errorf("no asset matching %q in release %s", want, release.TagName)
}

// ---------------------------------------------------------------------------
// GitHub API
// ---------------------------------------------------------------------------

// checkForUpdate queries GitHub for the latest release and returns it.
func checkForUpdate() (*githubRelease, error) {
	client := &http.Client{Timeout: updateHTTPTimeout}
	req, err := http.NewRequest("GET", githubReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("MeshMonitor/%s", Version))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api: HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("github api: decode: %w", err)
	}
	return &release, nil
}

// ---------------------------------------------------------------------------
// Download and replace
// ---------------------------------------------------------------------------

// downloadAndReplace downloads the binary from url and replaces the running executable.
func downloadAndReplace(url string, expectedSize int) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	tmpPath := execPath + ".new"
	defer os.Remove(tmpPath) // clean up on any failure path

	// Download to temp file.
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		return fmt.Errorf("download write: %w", err)
	}

	// Validate size if known.
	if expectedSize > 0 && int(written) != expectedSize {
		return fmt.Errorf("download incomplete: got %d bytes, expected %d", written, expectedSize)
	}

	// Atomic replace.
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Self-restart
// ---------------------------------------------------------------------------

// selfRestart re-executes the current binary with the same arguments.
func selfRestart() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for restart: %w", err)
	}
	ui.Info("Restarting with new version...")
	return syscall.Exec(execPath, os.Args, os.Environ())
}

// ---------------------------------------------------------------------------
// Orchestration
// ---------------------------------------------------------------------------

// performUpdate checks for an update, downloads it, and restarts.
// Returns false if no update was needed or if the update failed.
// On success, the process is replaced via exec and this function never returns.
func performUpdate() bool {
	release, err := checkForUpdate()
	if err != nil {
		ui.Warn("Update check failed: %v", err)
		return false
	}

	if !isNewerVersion(Version, release.TagName) {
		ui.Verb("Already up to date (%s).", Version)
		return false
	}

	url, size, err := findAsset(release)
	if err != nil {
		ui.Warn("Update: %v", err)
		return false
	}

	ui.Info("Updating %s → %s ...", Version, release.TagName)
	if err := downloadAndReplace(url, size); err != nil {
		ui.Warn("Update failed: %v", err)
		return false
	}

	ui.Success("Updated to %s", release.TagName)
	if err := selfRestart(); err != nil {
		ui.Error("Restart failed: %v — please restart manually", err)
		return false
	}
	// unreachable after successful exec
	return true
}

// runUpdateLoop runs periodic update checks in the background.
// Sends true on updateCh when an update is available and downloaded.
func runUpdateLoop(updateCh chan<- bool) {
	for {
		time.Sleep(cfg.UpdateInterval)
		if !cfg.AutoUpdate {
			continue
		}
		release, err := checkForUpdate()
		if err != nil {
			ui.Verb("Background update check failed: %v", err)
			continue
		}
		if isNewerVersion(Version, release.TagName) {
			ui.Info("New version available: %s (current: %s)", release.TagName, Version)
			updateCh <- true
			return // stop checking after signalling
		}
	}
}

// handleCheckUpdateFlag checks for updates, prints the result, and exits.
func handleCheckUpdateFlag() {
	ui.Info("MeshMonitor %s — checking for updates...", Version)
	release, err := checkForUpdate()
	if err != nil {
		ui.Error("Update check failed: %v", err)
		os.Exit(1)
	}
	if isNewerVersion(Version, release.TagName) {
		url, _, _ := findAsset(release)
		ui.Info("New version available: %s → %s", Version, release.TagName)
		if url != "" {
			ui.Info("Download: %s", url)
		}
	} else {
		ui.Success("Already up to date (%s).", Version)
	}
}
