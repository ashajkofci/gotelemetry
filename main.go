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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/albenik/go-serial"
)

// Definitions for the CRC16 computation
func CRC16(data []byte) uint16 {
	remainder := uint16(0)

	for _, b := range data {
		remainder = CRC16Recursive(b, remainder)
	}
	log.Printf("CRC16 calculated for data: %X -> %X", data, remainder)
	return remainder
}

func CRC16Recursive(byteVal byte, remainder uint16) uint16 {
	n := 16
	remainder ^= uint16(byteVal) << (n - 8)

	for j := 1; j < 8; j++ {
		if remainder&0x8000 != 0 {
			remainder = (remainder << 1) ^ 0x1021
		} else {
			remainder <<= 1
		}
		remainder &= 0xFFFF
	}
	return remainder
}

// Frame handling
const (
	SOF = 0xF7
	EOF = 0x7F
	ESC = 0x7D
)

type FramingState int

const (
	Idle FramingState = iota
	Escaping
	Active
)

type Frame struct {
	IncomingBuffer []byte
	OutgoingBuffer []byte
	IncomingState  FramingState
	OnFrame        func(data []byte)
	OnError        func(err error)
}

func NewFrame() *Frame {
	return &Frame{
		IncomingBuffer: make([]byte, 0),
		OutgoingBuffer: make([]byte, 0),
		IncomingState:  Idle,
		OnFrame:        nil,
		OnError:        nil,
	}
}

func (f *Frame) BeginFrame() {
	f.OutgoingBuffer = []byte{SOF}
	log.Println("Frame started.")
}

func (f *Frame) AppendByte(b byte) {
	if b == SOF || b == EOF || b == ESC {
		f.OutgoingBuffer = append(f.OutgoingBuffer, ESC)
	}
	f.OutgoingBuffer = append(f.OutgoingBuffer, b)
	log.Printf("Appended byte: %X", b)
}

func (f *Frame) AppendUint16(value uint16) {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, value)
	for _, b := range buf {
		f.AppendByte(b)
	}
	log.Printf("Appended uint16: %X", value)
}

func (f *Frame) EndFrame() {
	f.OutgoingBuffer = append(f.OutgoingBuffer, EOF)
	log.Println("Frame ended.")
}

func (f *Frame) FeedByte(b byte) {
	switch f.IncomingState {
	case Idle:
		if b == SOF {
			f.IncomingBuffer = []byte{}
			f.IncomingState = Active
		}
	case Escaping:
		f.IncomingBuffer = append(f.IncomingBuffer, b)
		f.IncomingState = Active
	case Active:
		if b == EOF {
			if f.OnFrame != nil {
				f.OnFrame(f.IncomingBuffer)
			}
			f.IncomingState = Idle
		} else if b == ESC {
			f.IncomingState = Escaping
		} else {
			f.IncomingBuffer = append(f.IncomingBuffer, b)
		}
	}
}

// Telemetry Core
const (
	IncomingBufferSize = 128
	OutgoingBufferSize = 128
	TopicBufferSize    = 64
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
	Read  func([]byte) (int, error)
	Write func([]byte) (int, error)
}

type Telemetry struct {
	Frame          *Frame
	HashTable      map[string]interface{}
	TopicCallbacks map[string]func(TMMsg)
	Transport      *TMTransport
	Mutex          sync.Mutex
	ReceivedTopics map[string]bool
}

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

func (t *Telemetry) Attach(topic string, variable interface{}) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	t.HashTable[topic] = variable
	log.Printf("Attached topic: %s", topic)
}

func (t *Telemetry) Subscribe(topic string, callback func(TMMsg)) {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	t.TopicCallbacks[topic] = callback
	log.Printf("Subscribed to topic: %s", topic)
}

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
	log.Printf("Published topic: %s, payload: %X", topic, payload)
	return nil
}

func (t *Telemetry) UpdateTelemetry() {
	go func() {
		buffer := make([]byte, IncomingBufferSize)
		for {
			n, err := t.Transport.Read(buffer)
			if err != nil {
				log.Printf("Error reading from transport: %v", err)
				continue
			}

			for i := 0; i < n; i++ {
				t.Frame.FeedByte(buffer[i])
			}

		}
	}()
}

func (t *Telemetry) TryUpdateHashTable(msg TMMsg) {

	t.Mutex.Lock()
	defer t.Mutex.Unlock()

	t.ReceivedTopics[msg.Topic] = true
	/*
		if callback, exists := t.TopicCallbacks[msg.Topic]; exists {
			callback(msg)
			return
		}
	*/
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
		log.Printf("Inserted topic: %s", msg.Topic)
	}

	if variable, ok := t.HashTable[msg.Topic]; ok {
		switch v := variable.(type) {
		case *float32:
			if msg.Type == TMFloat32 && len(msg.Payload) == 4 {
				bits := binary.LittleEndian.Uint32(msg.Payload)
				*v = math.Float32frombits(bits)
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
		//log.Printf("Updated topic: %s", msg.Topic)
	} else {
		//log.Printf("Topic not found: %s", msg.Topic)
	}
	t.PrintHashTable()
}

// PrintHashTable prints the current values stored in the telemetry's hash table.
func (t *Telemetry) PrintHashTable() {

	fmt.Println("Current Hash Table Values:")
	for topic, value := range t.HashTable {
		switch v := value.(type) {
		case *float32:
			fmt.Printf("Topic: %s, Value: %f\n", topic, *v)
		case *uint8:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *uint16:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *uint32:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *int8:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *int16:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *int32:
			fmt.Printf("Topic: %s, Value: %d\n", topic, *v)
		case *string:
			fmt.Printf("Topic: %s, Value: %s\n", topic, *v)
		default:
			fmt.Printf("Topic: %s, Value: Unknown Type\n", topic)
		}
	}
}

func (t *Telemetry) GetAvailableTopics() []string {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()

	topics := make([]string, 0, len(t.ReceivedTopics))
	for topic := range t.ReceivedTopics {
		topics = append(topics, topic)
	}
	return topics
}

func main() {
	// Configure serial port
	serialConfig := &serial.Mode{
		BaudRate: 115200,
	}

	port, err := serial.Open("COM5", serialConfig)
	if err != nil {
		log.Fatalf("Failed to open serial port: %v", err)
	}

	transport := &TMTransport{
		Read:  port.Read,
		Write: port.Write,
	}

	telemetry := NewTelemetry(transport)

	topic := "hello"
	// convert value to bytes
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, 51966)
	telemetry.Publish(topic, TMUint16, buf)

	telemetry.UpdateTelemetry()

	go func() {
		time.Sleep(5 * time.Second) // Wait for topics to accumulate
		topics := telemetry.GetAvailableTopics()
		log.Printf("Available topics: %v", topics)
		telemetry.PrintHashTable()
	}()

	select {}
}
