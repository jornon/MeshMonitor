package main

import "time"

// Version is set at build time via -ldflags.
var Version = "dev"

const (
	// Application identity — not user-configurable.
	AppName         = "MeshMonitor"
	ProtocolVersion = 8 // bumped from 3 to unlock CMD_GET_STATS, CMD_SET_FLOOD_SCOPE, etc.

	// Serial framing — fixed by the MeshCore protocol.
	BaudRate        = 115200
	MaxTxFrameSize  = 172 // max payload we send TO the device
	MaxRxFrameSize  = 300 // max payload we accept FROM the device (serial layer supports up to 300)

	// Command-response timeout — short fixed window for solicited responses.
	// The longer async timeouts (status, telemetry) live in cfg.
	CommandResponseTimeout = 5 * time.Second

	// Suggested timeout scaling — the firmware returns a suggested_timeout in
	// RESP_SENT. We scale it by dividing by 800 (roughly timeout_ms * 5/4),
	// matching the reference meshcore_py implementation.
	SuggestedTimeoutDivisor = 800

	// Minimum timeout floor — never wait less than this for async responses.
	MinAsyncTimeout = 10 * time.Second

	// MaxRetries for status/telemetry requests before giving up.
	MaxRequestRetries = 3

	// InterRequestDelay is the pause between status and telemetry requests
	// to the same repeater, avoiding TX queue flooding.
	InterRequestDelay = 2 * time.Second
)
