package gotelemetry

import (
	"context"
	"errors"
	"strings"
)

// errUSBDeviceEventsUnavailable makes sources poll while disconnected.
var errUSBDeviceEventsUnavailable = errors.New("USB device events are unavailable on this platform")

func newUSBSourceBackend(options USBTelemetrySourceOptions) usbSourceBackend {
	return usbSourceBackend{
		watch:    watchUSBDevices,
		discover: func(ctx context.Context) ([]USBDevice, error) { return discoverUSBDevices(ctx, options) },
		open: func(ctx context.Context, device USBDevice) (*usbSourceSession, error) {
			return openUSBSourceSession(ctx, device, options)
		},
	}
}

func discoverUSBDevices(ctx context.Context, options USBTelemetrySourceOptions) ([]USBDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ports, err := listUSBSerialPorts()
	if err != nil {
		return nil, err
	}
	var devices []USBDevice
	for _, port := range ports {
		if !strings.EqualFold(port.VendorID, options.VendorID) || !strings.EqualFold(port.ProductID, options.ProductID) {
			continue
		}
		devices = append(devices, USBDevice{
			Name: port.Name, SerialNumber: port.SerialNumber, Product: port.Product, Path: port.Path, Attached: port.Attached,
		})
	}
	return devices, ctx.Err()
}

func openUSBSourceSession(ctx context.Context, device USBDevice, options USBTelemetrySourceOptions) (*usbSourceSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transport, err := OpenSerialTransport(device.Name, options.Serial)
	if err != nil {
		return nil, err
	}
	return newUSBSourceSession(ctx, device, transport, options.Handshake)
}

func newUSBSourceSession(ctx context.Context, device USBDevice, transport *TMTransport, handshake *TMMsg) (*usbSourceSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	telemetry := NewTelemetry(transport)
	messages, done := make(chan TMMsg, 64), make(chan error, 1)
	stopped := make(chan struct{})
	telemetry.SetMessageHandler(func(msg TMMsg) {
		select {
		case messages <- msg:
		case <-sessionCtx.Done():
		}
	})
	go func() {
		defer close(stopped)
		done <- telemetry.Run(sessionCtx)
	}()
	session := &usbSourceSession{device: device, messages: messages, done: done, publish: telemetry.Publish, close: func() {
		cancel()
		<-stopped
	}}
	if handshake != nil {
		if err := telemetry.Publish(handshake.Topic, handshake.Type, handshake.Payload); err != nil {
			session.close()
			return nil, err
		}
	}
	return session, nil
}
