package gotelemetry

/*
 * Telemetry Library in Go
 *
 * This file is part of the Telemetry Library, a Go implementation compatible with
 * the [Overdrivr/Telemetry](https://github.com/Overdrivr/Telemetry) protocol.
 *
 * Features:
 * - CRC16 Validation (polynomial 0x1021, CRC-CCITT)
 * - Framing Protocol with Start-of-Frame (SOF), End-of-Frame (EOF), and Escape (ESC)
 * - Topic-based messaging for publishing, subscribing, and variable attachment
 * - Designed for serial communication
 *
 * License: MIT License
 * Author: Adrian Shajkofci, 2024
 */

import (
	"context"
	"errors"
	"strings"
	"time"
)

// usbSerialPort describes a serial port provided by a USB device. VendorID and
// ProductID are lowercase hexadecimal; Attached is zero when unknown.
type usbSerialPort struct {
	Name, Path                          string
	VendorID, ProductID                 string
	SerialNumber, Manufacturer, Product string
	Attached                            time.Time
}

const (
	VendorID  = "0403" // Default vendor for FTDI devices
	ProductID = "6015" // Default product for FTDI devices
)

// GetTransport scans for available USB ports and returns the first one that matches the specified VendorID and ProductID.
// All parameters are optional:
// - vendorId (default: VendorID constant "0403")
// - productId (default: ProductID constant "6015")
// - maxRetries (default: MaxRetries variable)
// - retryDelay (default: RetryDelay variable)
func GetTransport(options ...interface{}) (*TMTransport, error) {
	return GetTransportContext(context.Background(), options...)
}

// GetTransportContext performs legacy USB discovery with cancellable retries.
// A count of one performs a single attempt without a retry delay.
func GetTransportContext(ctx context.Context, options ...interface{}) (*TMTransport, error) {
	// Parse optional parameters with defaults
	vendorID, productID := VendorID, ProductID
	retries, delay := MaxRetries, RetryDelay
	for i, option := range options {
		switch i {
		case 0:
			if value, ok := option.(string); ok && value != "" {
				vendorID = value
			}
		case 1:
			if value, ok := option.(string); ok && value != "" {
				productID = value
			}
		case 2:
			if value, ok := option.(int); ok {
				retries = value
			}
		case 3:
			if value, ok := option.(time.Duration); ok {
				delay = value
			}
		}
	}
	if retries < 1 || delay < 0 {
		return nil, errors.New("retry count must be positive and retry delay nonnegative")
	}
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		transport, err := findUSBTransport(vendorID, productID)
		if err == nil {
			if ctx.Err() != nil {
				_ = transport.Close()
				return nil, ctx.Err()
			}
			return transport, nil
		}
		lastErr = err
		if attempt+1 < retries {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}

func findUSBTransport(vendorID, productID string) (*TMTransport, error) {
	ports, err := listUSBSerialPorts()
	if err != nil {
		return nil, err
	}
	for _, port := range ports {
		if !strings.EqualFold(port.VendorID, vendorID) || !strings.EqualFold(port.ProductID, productID) {
			continue
		}
		transport, err := OpenSerialTransport(port.Name, SerialTransportOptions{BaudRate: 115200})
		if err != nil {
			return nil, err
		}
		transport.VendorID, transport.ProductID = port.VendorID, port.ProductID
		transport.SerialNumber = port.SerialNumber
		transport.Manufacturer, transport.Product = port.Manufacturer, port.Product
		return transport, nil
	}
	return nil, errors.New("no matching USB port found")
}
