package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

type TMType uint16

const (
	TMString  TMType = 1
	TMUint8   TMType = 2
	TMUint16  TMType = 3
	TMUint32  TMType = 4
	TMInt8    TMType = 5
	TMInt16   TMType = 6
	TMInt32   TMType = 7
	TMFloat32 TMType = 8

	SOF = 0xF7
	EOF = 0x7F
	ESC = 0x7D

	HashSize           = 256
	IncomingBufferSize = 1024 * 128
	OutgoingBufferSize = 1024 * 128
	VendorID           = "0403"
	ProductID          = "6015"
)

type TMMessage struct {
	Topic string
	Type  TMType
	Data  []byte
}

type TMState struct {
	callbacks map[string]func(*TMMessage)
	variables map[string]interface{}
	sync.RWMutex
}

var (
	telemetryState = TMState{
		callbacks: make(map[string]func(*TMMessage)),
		variables: make(map[string]interface{}),
	}
	incomingBuffer = make([]byte, IncomingBufferSize)
	outgoingBuffer = make([]byte, OutgoingBufferSize)
	serialPort     io.ReadWriteCloser
	serialLock     sync.Mutex
)

func InitTelemetry() {
	fmt.Println("Telemetry initialized")
}

// Publish a message to the specified topic
func Publish(topic string, data interface{}) error {
	buf := new(bytes.Buffer)

	switch v := data.(type) {
	case string:
		buf.WriteString(v)
	case uint8, uint16, uint32, int8, int16, int32, float32:
		if err := binary.Write(buf, binary.LittleEndian, v); err != nil {
			return err
		}
	default:
		return errors.New("unsupported type")
	}

	frame := createFrame(topic, TMType(buf.Len()), buf.Bytes())
	return sendFrame(frame)
}

// Subscribe to a topic with a callback function
func subscribe(topic string, callback func(*TMMessage)) {
	telemetryState.Lock()
	telemetryState.callbacks[topic] = callback
	telemetryState.Unlock()
	fmt.Printf("[INFO] Subscribed to topic: %s\n", topic)
}

func createFrame(topic string, tType TMType, payload []byte) []byte {
	buf := new(bytes.Buffer)

	// SOF
	buf.WriteByte(SOF)

	// Header
	binary.Write(buf, binary.LittleEndian, tType)

	// Topic
	topicBytes := append([]byte(topic), 0) // Null-terminated
	buf.Write(topicBytes)

	// Payload
	buf.Write(payload)

	// CRC
	crc := calculateCRC(buf.Bytes()[1:]) // Exclude SOF
	binary.Write(buf, binary.LittleEndian, crc)

	// EOF
	buf.WriteByte(EOF)

	return buf.Bytes()
}

func calculateCRC(data []byte) uint16 {
	var rem uint16
	for _, b := range data {
		remainder := rem ^ (uint16(b) << 8)
		for i := 0; i < 8; i++ {
			if remainder&0x8000 != 0 {
				remainder = (remainder << 1) ^ 0x1021
			} else {
				remainder <<= 1
			}
		}
		rem = remainder & 0xFFFF
	}
	return rem
}

func sendFrame(frame []byte) error {
	serialLock.Lock()
	defer serialLock.Unlock()

	if serialPort == nil {
		var err error
		serialPort, _, err = GetUSBPort()
		if err != nil {
			return fmt.Errorf("failed to connect to serial port: %w", err)
		}
	}

	_, err := serialPort.Write(frame)
	if err != nil {
		serialPort.Close()
		serialPort = nil // Reset connection
		return err
	}
	return nil
}

func processFrame(frame []byte) {
	log.Printf("[INFO] Processing frame: %s\n", string(frame))
	if len(frame) < 6 {
		fmt.Println("[ERROR] Frame too small")
		return
	}

	if frame[0] != SOF || frame[len(frame)-1] != EOF {
		fmt.Println("[ERROR] Invalid SOF or EOF")
		return
	}

	buf := bytes.NewBuffer(frame[1 : len(frame)-3]) // Exclude SOF and EOF
	var tType TMType
	if err := binary.Read(buf, binary.LittleEndian, &tType); err != nil {
		fmt.Printf("[ERROR] Failed to read type: %v\n", err)
		return
	}

	topicBytes, err := buf.ReadBytes(0)
	if err != nil {
		fmt.Printf("[ERROR] Failed to read topic: %v\n", err)
		return
	}
	topic := string(topicBytes[:len(topicBytes)-1])

	payload := buf.Bytes()[:len(buf.Bytes())-2]
	crc := binary.LittleEndian.Uint16(buf.Bytes()[len(buf.Bytes())-2:])

	if crc != calculateCRC(frame[1:len(frame)-3]) {
		fmt.Println("[ERROR] CRC mismatch")
		return
	}

	telemetryState.RLock()
	callback, exists := telemetryState.callbacks[topic]
	telemetryState.RUnlock()
	if exists {
		msg := &TMMessage{Topic: topic, Type: tType, Data: payload}
		callback(msg)
	}
}

func GetUSBPort() (io.ReadWriteCloser, enumerator.PortDetails, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		log.Fatal(err)
	}
	if len(ports) == 0 {
		fmt.Println("No serial ports found!")
		return nil, enumerator.PortDetails{}, errors.New("no serial ports found")
	}
	for _, port := range ports {
		if port.IsUSB && port.VID == VendorID && port.PID == ProductID {
			mode := &serial.Mode{BaudRate: 115200}
			serPort, err := serial.Open(port.Name, mode)
			if err != nil {
				return nil, *port, err
			}
			return serPort, *port, nil
		}
	}
	return nil, enumerator.PortDetails{}, errors.New("no matching USB port found")
}

func UpdateTelemetry() {
	buffer := make([]byte, 256)
	for {
		serialLock.Lock()
		if serialPort == nil {
			serialLock.Unlock()
			time.Sleep(1 * time.Second)
			continue
		}
		n, err := serialPort.Read(buffer)
		serialLock.Unlock()
		if err != nil {
			fmt.Printf("[ERROR] Failed to read: %v\n", err)
			serialLock.Lock()
			serialPort.Close()
			serialPort = nil
			serialLock.Unlock()
			continue
		}
		processFrame(buffer[:n])
	}
}

func main() {
	InitTelemetry()

	/*subscribe("example", func(msg *TMMessage) {
		fmt.Printf("[INFO] Received message: %+v\n", msg)
	})*/

	go UpdateTelemetry()

	for {
		err := Publish("valve_command", uint8(4))
		if err != nil {
			fmt.Printf("[ERROR] Publish failed: %v\n", err)
		}
		time.Sleep(5 * time.Second)
	}
}
