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
package main

import (
	"errors"
	"log"

	"github.com/albenik/go-serial"
	"github.com/albenik/go-serial/enumerator"
)

const (
	VendorID  = "0403"
	ProductID = "6015"
)

func GetUSBPort() (serial.Port, *enumerator.PortDetails, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		log.Fatal(err)
	}
	if len(ports) == 0 {
		return nil, nil, errors.New("no serial ports found")
	}
	for _, port := range ports {
		if port.IsUSB && port.VID == VendorID && port.PID == ProductID {
			mode := &serial.Mode{
				BaudRate: 115200,
			}
			serPort, err := serial.Open(port.Name, mode)
			if err != nil {
				return nil, port, err
			}
			return serPort, port, nil
		}
	}
	return nil, nil, errors.New("no matching USB port found")
}
