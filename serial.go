package gotelemetry

import (
	"errors"
	"time"
)

// Parity selects the serial parity bit. The zero value is NoParity.
type Parity int

const (
	NoParity Parity = iota
	OddParity
	EvenParity
	MarkParity
	SpaceParity
)

// StopBits selects the number of serial stop bits. The zero value is OneStopBit.
type StopBits int

const (
	OneStopBit StopBits = iota
	TwoStopBits
)

// SerialTransportOptions configure a port without device-specific assumptions.
// Zero values select 9600 baud, 8N1 and one-second read/write timeouts. Timeouts
// must be finite and positive so Close can safely join pending I/O operations.
type SerialTransportOptions struct {
	BaudRate     int
	DataBits     int
	Parity       Parity
	StopBits     StopBits
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// OpenSerialTransport opens exactly the named port without discovery or retries.
// Serial ports are supported on Linux, Windows and macOS. Linux and macOS do
// not lock the port for exclusive use; Windows serial drivers only allow one
// open handle per port. Close is idempotent and joins pending I/O operations.
func OpenSerialTransport(name string, options SerialTransportOptions) (*TMTransport, error) {
	if options.BaudRate == 0 {
		options.BaudRate = 9600
	}
	if options.DataBits == 0 {
		options.DataBits = 8
	}
	if options.ReadTimeout == 0 {
		options.ReadTimeout = time.Second
	}
	if options.WriteTimeout == 0 {
		options.WriteTimeout = time.Second
	}
	if options.ReadTimeout < 0 || options.WriteTimeout < 0 {
		return nil, errors.New("serial timeouts must be positive")
	}
	return openSerialPort(name, options)
}
