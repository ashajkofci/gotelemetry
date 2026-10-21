//go:build darwin

package gotelemetry

import (
	"math"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSerialTermios(t *testing.T) {
	cases := []struct {
		name    string
		options SerialTransportOptions
		cflag   uint64 // Expected bits among CSIZE, PARENB, PARODD and CSTOPB.
		speed   uint64
		special bool
	}{
		{"115200 8N1", SerialTransportOptions{BaudRate: 115200, DataBits: 8}, unix.CS8, unix.B115200, false},
		{"7E2", SerialTransportOptions{BaudRate: 9600, DataBits: 7, Parity: EvenParity, StopBits: TwoStopBits}, unix.CS7 | unix.PARENB | unix.CSTOPB, unix.B9600, false},
		{"5O1", SerialTransportOptions{BaudRate: 300, DataBits: 5, Parity: OddParity}, unix.CS5 | unix.PARENB | unix.PARODD, unix.B300, false},
		{"non-standard rate", SerialTransportOptions{BaudRate: 250000, DataBits: 6}, unix.CS6, unix.B9600, true},
	}
	for _, c := range cases {
		// Start from a tty in canonical mode with flow control and echo.
		termios := unix.Termios{Iflag: math.MaxUint64, Oflag: math.MaxUint64, Cflag: math.MaxUint64, Lflag: math.MaxUint64}
		special, err := setSerialTermios(&termios, c.options)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		const framing = unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB
		const flow = unix.CRTSCTS | darwinCDTRIflow | darwinCDSROflow | darwinCCAROflow | unix.HUPCL
		inpck := uint64(0)
		if c.options.Parity != NoParity {
			inpck = unix.INPCK
		}
		if special != c.special || termios.Ispeed != c.speed || termios.Ospeed != c.speed ||
			termios.Cflag&framing != c.cflag || termios.Cflag&flow != 0 ||
			termios.Cflag&(unix.CREAD|unix.CLOCAL) != unix.CREAD|unix.CLOCAL ||
			termios.Iflag&(unix.IXON|unix.IXOFF|unix.ICRNL|unix.ISTRIP|unix.INPCK) != inpck ||
			termios.Oflag&unix.OPOST != 0 || termios.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 ||
			termios.Cc[unix.VMIN] != 1 || termios.Cc[unix.VTIME] != 0 {
			t.Errorf("%s: got %+v, special %v", c.name, termios, special)
		}
	}
	for _, options := range []SerialTransportOptions{
		{BaudRate: 9600, DataBits: 8, Parity: MarkParity},
		{BaudRate: 9600, DataBits: 8, Parity: SpaceParity},
		{BaudRate: 9600, DataBits: 4},
		{BaudRate: 9600, DataBits: 8, StopBits: StopBits(2)},
		{BaudRate: 0, DataBits: 8},
	} {
		var termios unix.Termios
		if _, err := setSerialTermios(&termios, options); err == nil {
			t.Errorf("%+v: expected an error", options)
		}
	}
}
