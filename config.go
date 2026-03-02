package main

import "time"

const (
	// Application identity — not user-configurable.
	AppName         = "MeshMonitor"
	ProtocolVersion = 3

	// Serial framing — fixed by the MeshCore protocol.
	BaudRate     = 115200
	MaxFrameSize = 172

	// Command-response timeout — short fixed window for solicited responses.
	// The longer async timeouts (status, telemetry) live in cfg.
	CommandResponseTimeout = 5 * time.Second
)
