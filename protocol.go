package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	txMarker = '<' // 0x3C — frames sent TO device   (confirmed from meshcore-cli)
	rxMarker = '>' // 0x3E — frames received FROM device
)

// SerialProtocol handles low-level MeshCore serial framing.
//
// Frame format (TX):  '>' | len_lo | len_hi | payload...
// Frame format (RX):  '<' | len_lo | len_hi | payload...
//
// A background goroutine reads frames continuously and routes them:
//   - Response frames (code < 0x80) → responseCh
//   - Push frames     (code >= 0x80) → pushCh
type SerialProtocol struct {
	port       serial.Port
	responseCh chan []byte
	pushCh     chan []byte
	mu         sync.Mutex // serialises writes
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewSerialProtocol opens the port, waits for the device to settle,
// and starts the background reader goroutine.
func NewSerialProtocol(portName string) (*SerialProtocol, error) {
	mode := &serial.Mode{BaudRate: BaudRate}
	p, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", portName, err)
	}

	// RTS must be de-asserted — confirmed by the official meshcore-cli Python client.
	// ESP32 USB-CDC devices may otherwise behave incorrectly (reset or refuse comms).
	if err := p.SetRTS(false); err != nil {
		ui.Warn("Could not set RTS=false: %v", err)
	}

	// 100 ms read timeout keeps the reader goroutine responsive to shutdown
	// and avoids blocking indefinitely when no data arrives.
	if err := p.SetReadTimeout(100 * time.Millisecond); err != nil {
		// Non-fatal: reader will still work, just won't exit quite as cleanly.
		ui.Warn("Could not set serial read timeout: %v", err)
	}

	sp := &SerialProtocol{
		port:       p,
		responseCh: make(chan []byte, 32),
		pushCh:     make(chan []byte, 32),
		done:       make(chan struct{}),
	}
	sp.wg.Add(1)
	go sp.readerLoop()

	// Give the device time to settle after the serial connection is established.
	// USB CDC devices (ESP32) may reset or flush buffers on connect; the reader
	// goroutine is already running so any initial noise is consumed and discarded.
	time.Sleep(1 * time.Second)
	sp.Flush()

	return sp, nil
}

// SendFrame writes a framed payload to the device.
func (sp *SerialProtocol) SendFrame(payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("payload too large: %d > %d", len(payload), MaxFrameSize)
	}
	header := []byte{txMarker, byte(len(payload) & 0xFF), byte(len(payload) >> 8)}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	_, err := sp.port.Write(append(header, payload...))
	return err
}

// WaitResponse blocks until a response frame arrives or the timeout expires.
func (sp *SerialProtocol) WaitResponse(timeout time.Duration) ([]byte, error) {
	select {
	case frame := <-sp.responseCh:
		return frame, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

// WaitResponseCode blocks until a response frame with the given code arrives.
// Frames with other codes are buffered and re-queued so they are not lost.
func (sp *SerialProtocol) WaitResponseCode(code byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var pending [][]byte

	defer func() {
		for _, f := range pending {
			select {
			case sp.responseCh <- f:
			default:
			}
		}
	}()

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		tick := remaining
		if tick > 100*time.Millisecond {
			tick = 100 * time.Millisecond
		}
		select {
		case frame := <-sp.responseCh:
			if frame[0] == code {
				return frame, nil
			}
			pending = append(pending, frame)
		case <-time.After(tick):
		}
	}
	return nil, fmt.Errorf("timeout waiting for response code 0x%02X", code)
}

// WaitPush blocks until a push frame with the given code arrives or the timeout expires.
// Push frames with other codes are buffered and re-queued.
func (sp *SerialProtocol) WaitPush(code byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var pending [][]byte

	defer func() {
		for _, p := range pending {
			select {
			case sp.pushCh <- p:
			default:
			}
		}
	}()

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		tick := remaining
		if tick > 500*time.Millisecond {
			tick = 500 * time.Millisecond
		}
		select {
		case frame := <-sp.pushCh:
			if frame[0] == code {
				return frame, nil
			}
			pending = append(pending, frame)
		case <-time.After(tick):
		}
	}
	return nil, fmt.Errorf("timeout waiting for push code 0x%02X", code)
}

// Flush discards all frames currently buffered in both channels.
// Call this before sending a fresh command sequence to avoid stale data.
func (sp *SerialProtocol) Flush() {
	for {
		select {
		case <-sp.responseCh:
		case <-sp.pushCh:
		default:
			return
		}
	}
}

// Close stops the reader goroutine and closes the serial port.
func (sp *SerialProtocol) Close() {
	close(sp.done)
	_ = sp.port.Close()
	sp.wg.Wait()
}

// ---------------------------------------------------------------------------
// Background reader goroutine
// ---------------------------------------------------------------------------

func (sp *SerialProtocol) readerLoop() {
	defer sp.wg.Done()

	const (
		stateIdle = 0
		stateLen1 = 1
		stateLen2 = 2
		stateData = 3
	)

	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	state := stateIdle
	frameLen := 0

	for {
		// Check for shutdown before each blocking read.
		select {
		case <-sp.done:
			return
		default:
		}

		n, err := sp.port.Read(tmp)
		if err != nil {
			if err == io.EOF {
				return
			}
			// Transient error (e.g. read timeout returning 0 bytes on some platforms).
			// Check for shutdown and retry.
			select {
			case <-sp.done:
				return
			default:
				continue
			}
		}
		if n == 0 {
			// Read timeout with no data — just loop and check done again.
			continue
		}
		buf = append(buf, tmp[:n]...)

		for len(buf) > 0 {
			switch state {
			case stateIdle:
				idx := -1
				for i, b := range buf {
					if b == rxMarker {
						idx = i
						break
					}
				}
				if idx < 0 {
					buf = buf[:0]
				} else {
					buf = buf[idx+1:]
					state = stateLen1
				}

			case stateLen1:
				if len(buf) < 1 {
					goto nextRead
				}
				frameLen = int(buf[0])
				buf = buf[1:]
				state = stateLen2

			case stateLen2:
				if len(buf) < 1 {
					goto nextRead
				}
				frameLen |= int(buf[0]) << 8
				buf = buf[1:]
				if frameLen == 0 || frameLen > MaxFrameSize {
					state = stateIdle
				} else {
					state = stateData
				}

			case stateData:
				if len(buf) < frameLen {
					goto nextRead
				}
				frame := make([]byte, frameLen)
				copy(frame, buf[:frameLen])
				buf = buf[frameLen:]
				state = stateIdle
				sp.routeFrame(frame)
			}
		}
	nextRead:
	}
}

func (sp *SerialProtocol) routeFrame(frame []byte) {
	if len(frame) == 0 {
		return
	}
	if frame[0] >= 0x80 {
		select {
		case sp.pushCh <- frame:
		default:
		}
	} else {
		select {
		case sp.responseCh <- frame:
		default:
		}
	}
}
