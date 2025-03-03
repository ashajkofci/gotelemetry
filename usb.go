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
	"errors"
	"log"
	"time"

	"github.com/albenik/go-serial/v2"
	"github.com/albenik/go-serial/v2/enumerator"
)

const (
	VendorID  = "0403" // Default vendor for FTDI devices
	ProductID = "6015" // Default product for FTDI devices
)

// GetTransport scans for available USB ports and returns the first one that matches the specified VendorID and ProductID.
func GetTransport(vendorId string, productId string) (*TMTransport, error) {
	var serPort *serial.Port
	var portDetails *enumerator.PortDetails
	var err error

	for i := 0; i < MaxRetries; i++ {
		serPort, portDetails, err = tryGetUSBPort(vendorId, productId)
		if err == nil {
			transport := &TMTransport{
				Read:         serPort.Read,
				Write:        serPort.Write,
				ProductID:    portDetails.PID,
				VendorID:     portDetails.VID,
				PortName:     portDetails.Name,
				Manufacturer: portDetails.Manufacturer,
				Product:      portDetails.Product,
				SerialNumber: portDetails.SerialNumber,
			}
			return transport, nil
		}
		log.Printf("Retrying to get USB port (%d/%d)...", i+1, MaxRetries)
		time.Sleep(RetryDelay)
	}

	return nil, err
}

func tryGetUSBPort(vendorId string, productId string) (*serial.Port, *enumerator.PortDetails, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, nil, err
	}
	if len(ports) == 0 {
		return nil, nil, errors.New("no serial ports found")
	}

	if vendorId == "" {
		vendorId = VendorID
	}
	if productId == "" {
		productId = ProductID
	}

	for _, port := range ports {
		if port.IsUSB && port.VID == vendorId && port.PID == productId {
			serPort, err := serial.Open(port.Name, serial.WithBaudrate(115200), serial.WithDataBits(8), serial.WithParity(serial.NoParity), serial.WithStopBits(serial.OneStopBit))
			serPort.SetReadTimeout(1000)
			if err != nil {
				return nil, port, err
			}
			return serPort, port, nil
		}
	}
	return nil, nil, errors.New("no matching USB port found")
}
