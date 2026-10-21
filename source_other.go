//go:build !linux && !windows && !darwin

package gotelemetry

import "context"

// Other platforms have no serial backend. Sources report it when discovering.
func watchUSBDevices(context.Context) (*usbSourceWatch, error) {
	return nil, errSerialUnsupported
}
