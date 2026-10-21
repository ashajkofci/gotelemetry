package gotelemetry

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestSerialTransportHardware exercises a real port named by
// GOTELEMETRY_TEST_PORT, such as COM5, /dev/ttyUSB0 or /dev/cu.usbserial-X.
// It only reads, so a streaming device and a silent port both pass.
func TestSerialTransportHardware(t *testing.T) {
	name := os.Getenv("GOTELEMETRY_TEST_PORT")
	if name == "" {
		t.Skip("set GOTELEMETRY_TEST_PORT to run against a serial port")
	}
	transport, err := OpenSerialTransport(name, SerialTransportOptions{BaudRate: 115200, ReadTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	start := time.Now()
	n, err := transport.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); n == 0 && (elapsed < 150*time.Millisecond || elapsed > 2*time.Second) {
		t.Fatalf("silent read returned after %v, want about 200ms", elapsed)
	}
	t.Logf("read %d bytes in %v", n, time.Since(start))
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}

	// Close must interrupt a read far from its timeout, and stay idempotent.
	transport, err = OpenSerialTransport(name, SerialTransportOptions{BaudRate: 115200, ReadTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := transport.Read(buffer); err != nil {
				done <- err
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	start = time.Now()
	closeErr := transport.Close()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("pending read returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not interrupt the pending read")
	}
	t.Logf("Close joined the pending read in %v", time.Since(start))
	if closeErr != nil || transport.Close() != closeErr {
		t.Fatalf("Close returned %v, then %v", closeErr, transport.Close())
	}
	if _, err := transport.Read(buffer); err == nil {
		t.Fatal("read after Close succeeded")
	}
	if _, err := transport.Write([]byte{0}); err == nil {
		t.Fatal("write after Close succeeded")
	}
}
