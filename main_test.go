package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListUSBPorts(t *testing.T) {
	port, portDetails, err := GetUSBPort()
	assert.Nil(t, err)
	assert.NotNil(t, portDetails)
	assert.NotNil(t, port)
	assert.Equal(t, "0403", portDetails.VID)
}

func TestInitTelemetry(t *testing.T) {

}
