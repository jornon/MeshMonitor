// Standalone diagnostic tool — connect to a MeshCore device and dump raw bytes.
// Usage: go run ./cmd/debug /dev/ttyUSB0
//
// Tries multiple strategies to elicit a response from the device.
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"go.bug.st/serial"
)

const baudRate = 115200

// readFor reads raw bytes from port for the given duration, printing every chunk.
// Returns total bytes received.
func readFor(p serial.Port, label string, dur time.Duration) int {
	buf := make([]byte, 256)
	deadline := time.Now().Add(dur)
	total := 0
	for time.Now().Before(deadline) {
		n, _ := p.Read(buf)
		if n > 0 {
			total += n
			ts := time.Now().Format("15:04:05.000")
			fmt.Printf("[%s][%s] rx %d bytes: %s\n", ts, label, n, hex.EncodeToString(buf[:n]))
		}
	}
	return total
}

func sendAppStart(p serial.Port) {
	// Exact bytes sent by official meshcore-cli:
	//   payload: 01 03 [6 spaces] mccli
	//   frame:   3c [len_lo] [len_hi] [payload]
	payload := []byte{0x01, 0x03, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 'm', 'c', 'c', 'l', 'i'}
	frame := []byte{0x3C, byte(len(payload)), 0x00}
	frame = append(frame, payload...)
	n, err := p.Write(frame)
	fmt.Printf("[debug] sent APP_START (%d bytes): %s  err=%v\n", n, hex.EncodeToString(frame), err)
}

func tryStrategy(name string, rts, dtr *bool, settleMs int, port string) int {
	fmt.Printf("\n=== Strategy: %s ===\n", name)
	p, err := serial.Open(port, &serial.Mode{BaudRate: baudRate})
	if err != nil {
		fmt.Printf("  open error: %v\n", err)
		return 0
	}
	defer p.Close()

	if rts != nil {
		if err := p.SetRTS(*rts); err != nil {
			fmt.Printf("  SetRTS(%v) err: %v\n", *rts, err)
		} else {
			fmt.Printf("  RTS=%v\n", *rts)
		}
	}
	if dtr != nil {
		if err := p.SetDTR(*dtr); err != nil {
			fmt.Printf("  SetDTR(%v) err: %v\n", *dtr, err)
		} else {
			fmt.Printf("  DTR=%v\n", *dtr)
		}
	}

	p.SetReadTimeout(200 * time.Millisecond)

	if settleMs > 0 {
		fmt.Printf("  settling %dms...\n", settleMs)
		// Read during settle - some devices send boot messages
		settleTotal := 0
		settleDeadline := time.Now().Add(time.Duration(settleMs) * time.Millisecond)
		buf := make([]byte, 256)
		for time.Now().Before(settleDeadline) {
			n, _ := p.Read(buf)
			if n > 0 {
				settleTotal += n
				fmt.Printf("  [settle] rx: %s\n", hex.EncodeToString(buf[:n]))
			}
		}
		if settleTotal > 0 {
			fmt.Printf("  settle: got %d bytes\n", settleTotal)
		}
	}

	sendAppStart(p)
	total := readFor(p, name, 5*time.Second)
	fmt.Printf("  response: %d bytes received\n", total)
	return total
}

func main() {
	port := "/dev/ttyUSB0"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	fmt.Printf("[debug] device: %s\n", port)

	f := false
	t := true

	// Try different strategies
	got := 0

	// Strategy 1: RTS=false, no settle (like Python client)
	got += tryStrategy("rts=false,nodtr,nosettle", &f, nil, 0, port)

	// Strategy 2: RTS=false, DTR=true, 2s settle
	got += tryStrategy("rts=false,dtr=true,2s", &f, &t, 2000, port)

	// Strategy 3: RTS=false, DTR=false, no settle
	got += tryStrategy("rts=false,dtr=false,nosettle", &f, &f, 0, port)

	// Strategy 4: No line changes, send immediately
	got += tryStrategy("no-line-change,nosettle", nil, nil, 0, port)

	// Strategy 5: RTS=true, DTR=true, 2s settle
	got += tryStrategy("rts=true,dtr=true,2s", &t, &t, 2000, port)

	fmt.Printf("\n=== Summary: %d total bytes received across all strategies ===\n", got)
	if got == 0 {
		fmt.Println("Device is completely silent. Possible issues:")
		fmt.Println("  - Device firmware does not have companion mode enabled")
		fmt.Println("  - Device is in bootloader/flash mode (try power-cycling)")
		fmt.Println("  - Wrong baud rate (MeshCore uses 115200)")
		fmt.Println("  - USB cable is charge-only (no data lines)")
	}
}
