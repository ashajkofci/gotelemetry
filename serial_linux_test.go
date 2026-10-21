//go:build linux

package gotelemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSerialTransportCancellationWithPTY(t *testing.T) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "ptmx")
	defer master.Close()
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("/dev/pts/%d", number)
	transport, err := OpenSerialTransport(name, SerialTransportOptions{BaudRate: 38400, ReadTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	// The port must not be locked for exclusive use.
	other, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	other.Close()
	telemetry := NewTelemetry(transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	received := make(chan struct{}, 1)
	telemetry.SetMessageHandler(func(TMMsg) { received <- struct{}{} })
	go func() { done <- telemetry.Run(ctx) }()
	if _, err := master.Write(testFrame(TMMsg{Topic: "pty", Type: TMUint8, Payload: []byte{1}})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("PTY telemetry not received")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("PTY reader did not stop")
	}
}
