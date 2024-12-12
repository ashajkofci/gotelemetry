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
package gotelemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestListUSBPorts tests the GetUSBPort function to ensure it correctly identifies a USB port with the specified VendorID and ProductID.
func TestListUSBPorts(t *testing.T) {
	port, portDetails, err := GetUSBPort()
	assert.Nil(t, err)
	assert.NotNil(t, portDetails)
	assert.NotNil(t, port)
	assert.Equal(t, "0403", portDetails.VID)
}

// TestInitTelemetry tests the initialization of the Telemetry system.
func TestInitTelemetry(t *testing.T) {

}
