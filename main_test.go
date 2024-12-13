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
	"time"

	"github.com/stretchr/testify/assert"
)

// MockTransport simulates the TMTransport for testing purposes.
type MockTransport struct {
	ReadBuffer  []byte
	WriteBuffer []byte
}

func (m *MockTransport) Read(p []byte) (int, error) {
	n := copy(p, m.ReadBuffer)
	m.ReadBuffer = m.ReadBuffer[n:]
	return n, nil
}

func (m *MockTransport) Write(p []byte) (int, error) {
	m.WriteBuffer = append(m.WriteBuffer, p...)
	return len(p), nil
}

// TestNewTelemetry tests the creation of a new Telemetry instance.
func TestNewTelemetry(t *testing.T) {
	mockTransport := &MockTransport{}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	assert.NotNil(t, telemetry)
	assert.NotNil(t, telemetry.Frame)
	assert.NotNil(t, telemetry.HashTable)
	assert.NotNil(t, telemetry.TopicCallbacks)
	assert.NotNil(t, telemetry.Transport)
	assert.NotNil(t, telemetry.ReceivedTopics)
}

// TestPublish tests the Publish method of the Telemetry struct.
func TestPublish(t *testing.T) {
	mockTransport := &MockTransport{}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	err := telemetry.Publish("test_topic", TMUint8, []byte{42})
	assert.Nil(t, err)
	assert.NotEmpty(t, mockTransport.WriteBuffer)
}

// TestSubscribe tests the Subscribe method of the Telemetry struct.
func TestSubscribe(t *testing.T) {
	mockTransport := &MockTransport{}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})
	var receivedMsg TMMsg
	telemetry.Subscribe("test_topic", func(msg TMMsg) {
		receivedMsg = msg
	})

	telemetry.TryUpdateHashTable(TMMsg{
		Type:    TMUint8,
		Topic:   "test_topic",
		Payload: []byte{42},
	})

	assert.Equal(t, "test_topic", receivedMsg.Topic)
	assert.Equal(t, TMUint8, receivedMsg.Type)
	assert.Equal(t, []byte{42}, receivedMsg.Payload)
}

// TestAttach tests the Attach method of the Telemetry struct.
func TestAttach(t *testing.T) {
	mockTransport := &MockTransport{}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	var value uint8
	telemetry.Attach("test_topic", &value)

	telemetry.TryUpdateHashTable(TMMsg{
		Type:    TMUint8,
		Topic:   "test_topic",
		Payload: []byte{42},
	})

	assert.Equal(t, uint8(42), value)
}

// TestUpdateTelemetry tests the UpdateTelemetry method of the Telemetry struct.
func TestUpdateTelemetry(t *testing.T) {
	// Prepare a valid frame with correct CRC
	payload := []byte{42}
	topic := "test_topic"
	msgType := TMUint8
	topicBytes := append([]byte(topic), 0)
	frameData := append([]byte{SOF, byte(msgType), 0}, topicBytes...)
	frameData = append(frameData, payload...)
	crc := CRC16(frameData[1:])
	frameData = append(frameData, byte(crc), byte(crc>>8), EOF)

	mockTransport := &MockTransport{
		ReadBuffer: frameData,
	}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	var receivedMsg TMMsg
	telemetry.Subscribe("test_topic", func(msg TMMsg) {
		receivedMsg = msg
	})
	stopChan := make(chan struct{})
	telemetry.UpdateTelemetry(stopChan)

	// Simulate reading from the transport
	buffer := make([]byte, IncomingBufferSize)
	n, err := mockTransport.Read(buffer)
	assert.Nil(t, err)
	for i := 0; i < n; i++ {
		telemetry.Frame.FeedByte(buffer[i])
	}

	// Ensure the frame is processed
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, "test_topic", receivedMsg.Topic)
	assert.Equal(t, TMUint8, receivedMsg.Type)
	assert.Equal(t, []byte{42}, receivedMsg.Payload)
}

// TestUpdateTelemetryStopChannel tests the UpdateTelemetry method with a stop channel.
func TestUpdateTelemetryStopChannel(t *testing.T) {
	mockTransport := &MockTransport{
		ReadBuffer: []byte{SOF, 0x01, 0x00, 't', 'e', 's', 't', 0x00, 0x2A, 0x00, 0x00, EOF},
	}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	stopChan := make(chan struct{})
	go telemetry.UpdateTelemetry(stopChan)

	// Allow some time for the telemetry to process
	time.Sleep(10 * time.Millisecond)

	// Stop the telemetry update
	close(stopChan)

	// Allow some time for the telemetry to stop
	time.Sleep(10 * time.Millisecond)

	// Ensure the telemetry has stopped by checking if the read buffer is empty
	assert.Empty(t, mockTransport.ReadBuffer)
}

// TestGeneralCallbackOnHashTableChange tests that the general callback is triggered only when the hash table value changes.
func TestGeneralCallbackOnHashTableChange(t *testing.T) {
	mockTransport := &MockTransport{}
	telemetry := NewTelemetry(&TMTransport{
		Read:  mockTransport.Read,
		Write: mockTransport.Write,
	})

	var callbackTriggered bool
	telemetry.GeneralCallback = func(msg TMMsg) {
		callbackTriggered = true
	}

	var value uint8
	telemetry.Attach("test_topic", &value)

	// First update should trigger the callback
	telemetry.TryUpdateHashTable(TMMsg{
		Type:    TMUint8,
		Topic:   "test_topic",
		Payload: []byte{42},
	})
	assert.True(t, callbackTriggered)

	// Reset the flag
	callbackTriggered = false

	// Second update with the same value should not trigger the callback
	telemetry.TryUpdateHashTable(TMMsg{
		Type:    TMUint8,
		Topic:   "test_topic",
		Payload: []byte{42},
	})
	assert.False(t, callbackTriggered)

	// Update with a different value should trigger the callback
	telemetry.TryUpdateHashTable(TMMsg{
		Type:    TMUint8,
		Topic:   "test_topic",
		Payload: []byte{43},
	})
	assert.True(t, callbackTriggered)
}
