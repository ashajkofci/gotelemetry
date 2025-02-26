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
	"log"
	"math"
	"reflect"
	"sync"
	"time"
)

// Telemetry Core
const (
	IncomingBufferSize = 128
	OutgoingBufferSize = 128
	TopicBufferSize    = 64
	MaxRetries         = 1000
	RetryDelay         = 10 * time.Second
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
	}
	log.Println("Telemetry system initialized.")
	return t
}

// parseFrame parses a received frame into a TMMsg.
func (t *Telemetry) parseFrame(data []byte) (TMMsg, error) {

	if len(data) < 4 { // Minimum length to include header and topic
		return TMMsg{}, errors.New("frame too short after unescaping")
	}

	msgType := binary.LittleEndian.Uint16(data[:2])
	topicEnd := bytes.IndexByte(data[2:], 0) + 2
	if topicEnd < 2 {
		return TMMsg{}, errors.New("invalid topic")
	}
	topic := string(data[2:topicEnd])
	payload := data[topicEnd+1 : len(data)-2]
	crcLocal := CRC16(data[:len(data)-2])
	crcFrame := binary.LittleEndian.Uint16(data[len(data)-2:])

	if crcLocal != crcFrame {
		return TMMsg{}, fmt.Errorf("CRC mismatch: local=%X frame=%X", crcLocal, crcFrame)
	}

	return TMMsg{
		Type:    TMType(msgType),
		Topic:   topic,
		Payload: payload,
	}, nil
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
	t.Mutex.Lock()
	defer t.Mutex.Unlock()

	t.Frame.BeginFrame()
	head := make([]byte, 2)
	binary.LittleEndian.PutUint16(head, uint16(msgType))
	t.Frame.AppendByte(head[0])
	t.Frame.AppendByte(head[1])

	topicBytes := []byte(topic)
	t.Frame.OutgoingBuffer = append(t.Frame.OutgoingBuffer, topicBytes...)
	t.Frame.AppendByte(0)

	t.Frame.OutgoingBuffer = append(t.Frame.OutgoingBuffer, payload...)

	crc := CRC16(t.Frame.OutgoingBuffer[1:])
	t.Frame.AppendUint16(crc)
	t.Frame.EndFrame()

	_, err := t.Transport.Write(t.Frame.OutgoingBuffer)
	if err != nil {
		log.Printf("Failed to publish topic: %s, error: %v", topic, err)
		return err
	}
	return nil
}

// UpdateTelemetry starts listening for incoming messages and processes them.
func (t *Telemetry) UpdateTelemetry(stopChan chan struct{}) {
	go func() {
		buffer := make([]byte, IncomingBufferSize)
		for {
			select {
			case <-stopChan:
				log.Println("Stopping telemetry update.")
				return
			default:
				n, err := t.Transport.Read(buffer)
				if err != nil {
					log.Printf("Error reading from transport: %v", err)
					t.reconnect()
					continue
				}

				for i := 0; i < n; i++ {
					t.Frame.FeedByte(buffer[i])
				}
			}
		}
	}()
}

// reconnect attempts to re-establish the USB connection.
func (t *Telemetry) reconnect() {
	log.Println("Attempting to reconnect...")
	for i := 0; i < MaxRetries; i++ {
		transport, err := GetTransport(t.Transport.VendorID, t.Transport.ProductID)
		if err == nil {
			t.Transport = transport
			log.Printf("Reconnected to USB port: %s", transport.PortName)
			return
		}
		log.Printf("Retrying to reconnect (%d/%d)...", i+1, MaxRetries)
		time.Sleep(RetryDelay)
	}
	log.Println("Failed to reconnect after maximum retries.")
}

// TryUpdateHashTable updates the hash table with the received message and calls the appropriate callbacks.
func (t *Telemetry) TryUpdateHashTable(msg TMMsg) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()

	t.ReceivedTopics[msg.Topic] = true

	// if the topic is not found in the hash table, insert it
	if _, ok := t.HashTable[msg.Topic]; !ok {
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

	// Use specific callback if it exists and value is changed
	if callback, exists := t.TopicCallbacks[msg.Topic]; valueChanged && exists {
		callback(msg)
		return
	}

	// Use general callback only if the value has changed
	if valueChanged && t.GeneralCallback != nil {
		t.GeneralCallback(msg)
	}
}

// GetValue returns the value of a topic in the hash table.
func (t *Telemetry) GetValue(topic string) interface{} {
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
