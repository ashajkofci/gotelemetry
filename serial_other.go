//go:build !linux && !windows && !darwin

package gotelemetry

import "errors"

var errSerialUnsupported = errors.New("serial ports are only supported on Linux, Windows and macOS")

func openSerialPort(string, SerialTransportOptions) (*TMTransport, error) {
	return nil, errSerialUnsupported
}

func listUSBSerialPorts() ([]usbSerialPort, error) {
	return nil, errSerialUnsupported
}
