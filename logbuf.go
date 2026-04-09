package main

import (
	"fmt"
	"sync"
	"time"
)

// LogEntry is a single diagnostic log record for remote collection.
type LogEntry struct {
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"`
	Tag       string `json:"tag"`
	Message   string `json:"message"`
}

// LogBuffer is a thread-safe ring buffer of log entries.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	maxSize int
}

var logBuf = &LogBuffer{maxSize: 500}

// Log appends an entry to the buffer, dropping the oldest if full.
func (b *LogBuffer) Log(level, tag, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) > 4096 {
		msg = msg[:4096]
	}
	if len(tag) > 64 {
		tag = tag[:64]
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= b.maxSize {
		b.entries = b.entries[1:]
	}
	b.entries = append(b.entries, LogEntry{
		Timestamp: time.Now().Unix(),
		Level:     level,
		Tag:       tag,
		Message:   msg,
	})
}

// Drain returns all buffered entries and clears the buffer.
func (b *LogBuffer) Drain() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) == 0 {
		return nil
	}
	out := b.entries
	b.entries = make([]LogEntry, 0, b.maxSize)
	return out
}
