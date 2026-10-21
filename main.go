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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Telemetry Core
const (
	IncomingBufferSize = 128
	OutgoingBufferSize = 128
	TopicBufferSize    = 64
)

var (
	MaxRetries = 1000             // Maximum number of retries for connections
	RetryDelay = 10 * time.Second // Delay between retry attempts
)

type TMType int

const (
	TMFloat32 TMType = iota
	TMUint8
	TMUint16
	TMUint32
	TMInt8
	TMInt16
	TMInt32
	TMString
)

type TMMsg struct {
	Type    TMType
	Topic   string
	Payload []byte
}

type TMTransport struct {
	Read         func([]byte) (int, error)
	Write        func([]byte) (int, error)
	ProductID    string
	VendorID     string
	Manufacturer string
	Product      string
	SerialNumber string
	PortName     string
	// Close releases the transport and joins pending I/O operations.
	Close func() error
}

// Telemetry represents the core telemetry system, handling frame parsing, topic-based messaging, and CRC validation.
type Telemetry struct {
	// Frame handles the framing protocol for incoming and outgoing messages.
	Frame *Frame
	// HashTable stores the values of attached variables by topic.
	HashTable map[string]interface{}
	// TopicCallbacks stores the callbacks for specific topics.
	TopicCallbacks map[string]func(TMMsg)
	// GeneralCallback is called for any received message if no specific topic callback is registered.
	GeneralCallback func(TMMsg)
	// Transport handles the read and write operations for the telemetry system.
	Transport *TMTransport
	// Mutex ensures thread-safe access to the telemetry system's data structures.
	Mutex sync.Mutex
	// ReceivedTopics keeps track of all received topics.
	ReceivedTopics map[string]bool

	// messageHandler observes all decoded messages without changing subscriptions.
	messageHandler func(TMMsg)
	writeMu        sync.Mutex
	runMu          sync.Mutex
}

// NewTelemetry creates a new telemetry instance with the provided transport.
func NewTelemetry(transport *TMTransport) *Telemetry {
	t := &Telemetry{
		Frame:          NewFrame(),
		HashTable:      make(map[string]interface{}),
		TopicCallbacks: make(map[string]func(TMMsg)),
		Transport:      transport,
		ReceivedTopics: make(map[string]bool),
	}
	t.Frame.OnFrame = func(data []byte) {
		msg, err := t.parseFrame(data)
		if err != nil {
			log.Printf("Error parsing frame: %v", err)
			return
		}
		t.TryUpdateHashTable(msg)
		t.Mutex.Lock()
		handler := t.messageHandler
		t.Mutex.Unlock()
		if handler != nil {
			handler(msg)
		}
	}
	log.Println("Telemetry system initialized.")
	return t
}

// parseFrame parses a received frame into a TMMsg.
func (t *Telemetry) parseFrame(data []byte) (TMMsg, error) {
	// Minimum length to include header and topic, plus the CRC.
	if len(data) < 5 {
		return TMMsg{}, errors.New("frame too short after unescaping")
	}
	topicEnd := bytes.IndexByte(data[2:len(data)-2], 0)
	if topicEnd < 0 {
		return TMMsg{}, errors.New("invalid topic")
	}
	topicEnd += 2
	crcLocal := CRC16(data[:len(data)-2])
	crcFrame := binary.LittleEndian.Uint16(data[len(data)-2:])
	if crcLocal != crcFrame {
		return TMMsg{}, fmt.Errorf("CRC mismatch: local=%X frame=%X", crcLocal, crcFrame)
	}
	msg := TMMsg{
		Type:    TMType(binary.LittleEndian.Uint16(data[:2])),
		Topic:   string(data[2:topicEnd]),
		Payload: append([]byte(nil), data[topicEnd+1:len(data)-2]...),
	}
	if _, err := msg.Value(); err != nil {
		return TMMsg{}, err
	}
	return msg, nil
}

// Attach links a variable to a topic for automatic updates.
func (t *Telemetry) Attach(topic string, variable interface{}) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	t.HashTable[topic] = variable
	log.Printf("Attached topic: %s", topic)
}

// Subscribe registers a callback for a specific topic. If the topic is an empty string, the callback is called for any received message.
func (t *Telemetry) Subscribe(topic string, callback func(TMMsg)) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	if topic == "" {
		t.GeneralCallback = callback
		log.Printf("Subscribed to all topics")
		return
	}
	t.TopicCallbacks[topic] = callback
	log.Printf("Subscribed to topic: %s", topic)
}

// Publish sends a message to a topic with the specified type and payload.
func (t *Telemetry) Publish(topic string, msgType TMType, payload []byte) error {
	if strings.IndexByte(topic, 0) >= 0 {
		return errors.New("topic contains a null byte")
	}
	if _, err := (TMMsg{Type: msgType, Payload: payload}).Value(); err != nil {
		return err
	}
	// Sending and receiving use separate frame buffers, so callbacks may publish.
	data := make([]byte, 2, 2+len(topic)+1+len(payload))
	binary.LittleEndian.PutUint16(data, uint16(msgType))
	data = append(data, topic...)
	data = append(data, 0)
	data = append(data, payload...)
	frame := NewFrame()
	frame.BeginFrame()
	for _, b := range data {
		frame.AppendByte(b)
	}
	frame.AppendUint16(CRC16(data))
	frame.EndFrame()

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.Mutex.Lock()
	transport := t.Transport
	t.Mutex.Unlock()
	if transport == nil || transport.Write == nil {
		return errors.New("transport has no writer")
	}
	n, err := transport.Write(frame.OutgoingBuffer)
	if err == nil && n != len(frame.OutgoingBuffer) {
		err = io.ErrShortWrite
	}
	return err
}

// TryUpdateHashTable updates the hash table with the received message and calls the appropriate callbacks.
func (t *Telemetry) TryUpdateHashTable(msg TMMsg) {
	t.Mutex.Lock()

	t.ReceivedTopics[msg.Topic] = true

	// Check if this is a new topic
	isNewTopic := false
	if _, ok := t.HashTable[msg.Topic]; !ok {
		isNewTopic = true
		switch msg.Type {
		case TMFloat32:
			t.HashTable[msg.Topic] = new(float32)
		case TMUint8:
			t.HashTable[msg.Topic] = new(uint8)
		case TMUint16:
			t.HashTable[msg.Topic] = new(uint16)
		case TMUint32:
			t.HashTable[msg.Topic] = new(uint32)
		case TMInt8:
			t.HashTable[msg.Topic] = new(int8)
		case TMInt16:
			t.HashTable[msg.Topic] = new(int16)
		case TMInt32:
			t.HashTable[msg.Topic] = new(int32)
		case TMString:
			t.HashTable[msg.Topic] = new(string)
		default:
			log.Printf("Unknown topic type: %d", msg.Type)
		}
	}

	// Check if the value has changed before updating
	valueChanged := false
	if variable, ok := t.HashTable[msg.Topic]; ok {
		newValue := reflect.New(reflect.TypeOf(variable).Elem()).Interface()
		switch v := newValue.(type) {
		case *float32:
			if msg.Type == TMFloat32 && len(msg.Payload) == 4 {
				*v = math.Float32frombits(binary.LittleEndian.Uint32(msg.Payload))
			}
		case *uint8:
			if msg.Type == TMUint8 && len(msg.Payload) == 1 {
				*v = msg.Payload[0]
			}
		case *uint16:
			if msg.Type == TMUint16 && len(msg.Payload) == 2 {
				*v = binary.LittleEndian.Uint16(msg.Payload)
			}
		case *uint32:
			if msg.Type == TMUint32 && len(msg.Payload) == 4 {
				*v = binary.LittleEndian.Uint32(msg.Payload)
			}
		case *int8:
			if msg.Type == TMInt8 && len(msg.Payload) == 1 {
				*v = int8(msg.Payload[0])
			}
		case *int16:
			if msg.Type == TMInt16 && len(msg.Payload) == 2 {
				*v = int16(binary.LittleEndian.Uint16(msg.Payload))
			}
		case *int32:
			if msg.Type == TMInt32 && len(msg.Payload) == 4 {
				*v = int32(binary.LittleEndian.Uint32(msg.Payload))
			}
		case *string:
			if msg.Type == TMString {
				*v = string(msg.Payload)
			}
		default:
			log.Printf("Unknown topic type: %T", v)
		}

		if !reflect.DeepEqual(variable, newValue) {
			reflect.ValueOf(variable).Elem().Set(reflect.ValueOf(newValue).Elem())
			valueChanged = true
		}
	}

	// If this is a new topic or the value has changed, trigger callbacks
	shouldTriggerCallback := isNewTopic || valueChanged

	// Snapshot callbacks under the mutex, then release it before invoking them.
	// This preserves routing while allowing callbacks to query or subscribe.
	callback, exists := t.TopicCallbacks[msg.Topic]
	generalCallback := t.GeneralCallback
	t.Mutex.Unlock()

	// Use specific callback if it exists
	if shouldTriggerCallback && exists {
		callback(msg)
		return
	}

	// Use general callback
	if shouldTriggerCallback && generalCallback != nil {
		generalCallback(msg)
	}
}

// GetValue returns the value of a topic in the hash table.
func (t *Telemetry) GetValue(topic string) interface{} {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	// if it's a pointer to a variable, return the value
	if value, ok := t.HashTable[topic]; ok {
		switch v := value.(type) {
		case *float32:
			return *v
		case *uint8:
			return *v
		case *uint16:
			return *v
		case *uint32:
			return *v
		case *int8:
			return *v
		case *int16:
			return *v
		case *int32:
			return *v
		case *string:
			return *v
		default:
			log.Printf("Unknown topic type: %T", v)
			return nil
		}
	}
	return nil
}

// PrintHashTable prints the current values stored in the telemetry's hash table.
func (t *Telemetry) PrintHashTable() {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	fmt.Println("Current Hash Table Values:")
	formatMap := map[string]string{
		"*float32": "%f",
		"*uint8":   "%d",
		"*uint16":  "%d",
		"*uint32":  "%d",
		"*int8":    "%d",
		"*int16":   "%d",
		"*int32":   "%d",
		"*string":  "%s",
	}

	for topic, value := range t.HashTable {
		valueType := fmt.Sprintf("%T", value)
		if format, ok := formatMap[valueType]; ok {
			fmt.Printf("Topic: %s, Value: "+format+"\n", topic, reflect.ValueOf(value).Elem())
		} else {
			fmt.Printf("Topic: %s, Value: Unknown Type\n", topic)
		}
	}
}

// GetAvailableTopics returns a list of all received topics.
func (t *Telemetry) GetAvailableTopics() []string {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()

	topics := make([]string, 0, len(t.ReceivedTopics))
	for topic := range t.ReceivedTopics {
		topics = append(topics, topic)
	}
	return topics
}

// SetMessageHandler receives every valid decoded message, including unchanged
// values, after change subscriptions. It does not change Subscribe behavior.
// Handlers run synchronously outside the mutex and should return promptly.
func (t *Telemetry) SetMessageHandler(callback func(TMMsg)) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	t.messageHandler = callback
}
