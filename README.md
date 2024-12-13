# Telemetry Library in Go

This repository provides a Go implementation of a telemetry system based on framing, CRC validation, and topic-based message handling. It is compatible with the [Overdrivr/Telemetry](https://github.com/Overdrivr/Telemetry) protocol, making it interoperable with Python implementations. It is designed for serial communication and supports attaching variables, subscribing to topics, and publishing data.

## Features

- **CRC16 Validation**: Ensures message integrity with a 16-bit cyclic redundancy check.
- **Framing Protocol**: Supports custom start-of-frame (SOF), end-of-frame (EOF), and escape sequences.
- **Topic-Based Messaging**:
  - Attach variables to topics.
  - Subscribe to topics with callbacks.
  - Publish messages to topics.
- **Serial Communication**: Uses `github.com/albenik/go-serial` for serial port interaction.

## Installation

Clone the repository and ensure you have Go installed.

```bash
# Clone the repository
git clone <repository-url>
cd <repository>

# Build the project
go build
```

## Usage

### Import the Library

```go
import "path/to/telemetry"
```

### Example

```go
package main

import (
	"log"
	"time"
	"path/to/telemetry"
)

func main() {
	// Get the transport for the USB device
	transport, err := telemetry.GetTransport("", "")
	if err != nil {
		log.Fatalf("Failed to get USB transport: %v", err)
	}

	// Initialize telemetry
	tele := telemetry.NewTelemetry(transport)

	// Attach a variable to a topic
	value := uint8(42)
	tele.Attach("example_topic", &value)

	// Subscribe to a topic
	tele.Subscribe("example_topic", func(msg telemetry.TMMsg) {
		log.Printf("Received message on topic '%s': %X", msg.Topic, msg.Payload)
	})

	// Publish a message
	err = tele.Publish("example_topic", telemetry.TMUint8, []byte{value})
	if err != nil {
		log.Printf("Failed to publish: %v", err)
	}

	// Start listening for incoming messages
	stopChan := make(chan struct{})
	tele.UpdateTelemetry(stopChan)

	// Keep the program running
	select {}
}
```

### Attaching variables to topics
```go
package main

import (
    "log"
    "path/to/telemetry"
)

func main() {
    // Initialize telemetry with a mock transport for testing
    mockTransport := &telemetry.MockTransport{}
    tele := telemetry.NewTelemetry(&telemetry.TMTransport{
        Read:  mockTransport.Read,
        Write: mockTransport.Write,
    })

    // Attach variables to topics
    var intValue int32
    var floatValue float32
    tele.Attach("int_topic", &intValue)
    tele.Attach("float_topic", &floatValue)

    // Simulate receiving messages
    tele.TryUpdateHashTable(telemetry.TMMsg{
        Type:    telemetry.TMInt32,
        Topic:   "int_topic",
        Payload: []byte{0, 0, 0, 42},
    })
    tele.TryUpdateHashTable(telemetry.TMMsg{
        Type:    telemetry.TMFloat32,
        Topic:   "float_topic",
        Payload: []byte{0, 0, 128, 63},
    })

    log.Printf("int_topic value: %d", intValue)
    log.Printf("float_topic value: %f", floatValue)
}
```

### Publishing and subscribing to multiple topics
```go
package main

import (
    "log"
    "path/to/telemetry"
)

func main() {
    // Initialize telemetry with a mock transport for testing
    mockTransport := &telemetry.MockTransport{}
    tele := telemetry.NewTelemetry(&telemetry.TMTransport{
        Read:  mockTransport.Read,
        Write: mockTransport.Write,
    })

    // Subscribe to multiple topics
    tele.Subscribe("topic1", func(msg telemetry.TMMsg) {
        log.Printf("Received on topic1: %s", msg.Payload)
    })
    tele.Subscribe("topic2", func(msg telemetry.TMMsg) {
        log.Printf("Received on topic2: %s", msg.Payload)
    })

    // Publish messages to different topics
    tele.Publish("topic1", telemetry.TMString, []byte("Hello Topic 1"))
    tele.Publish("topic2", telemetry.TMString, []byte("Hello Topic 2"))

    // Start listening for incoming messages
    stopChan := make(chan struct{})
    tele.UpdateTelemetry(stopChan)

    // Keep the program running
    select {}
}
```

## API Documentation

### `Telemetry` Structure

- `NewTelemetry(transport *TMTransport) *Telemetry`:
  Creates a new telemetry instance.

- `Attach(topic string, variable interface{})`:
  Links a variable to a topic for automatic updates.

- `Subscribe(topic string, callback func(TMMsg))`:
  Registers a callback for a specific topic.

- `Publish(topic string, msgType TMType, payload []byte) error`:
  Sends a message to a topic.

- `UpdateTelemetry(stopChan chan struct{})`:
  Starts listening for incoming messages.

- `PrintHashTable()`:
  Prints the current values in the hash table.

### Message Types (`TMType`)

| Type       | Description     |
|------------|-----------------|
| `TMFloat32`| 32-bit float    |
| `TMUint8`  | Unsigned 8-bit  |
| `TMUint16` | Unsigned 16-bit |
| `TMUint32` | Unsigned 32-bit |
| `TMInt8`   | Signed 8-bit    |
| `TMInt16`  | Signed 16-bit   |
| `TMInt32`  | Signed 32-bit   |
| `TMString` | String          |

## Framing Protocol

- **Start of Frame (SOF)**: `0xF7`
- **End of Frame (EOF)**: `0x7F`
- **Escape Character (ESC)**: `0x7D`

## CRC16 Validation

Uses the polynomial `0x1021` (CRC-CCITT). Both Python and Go implementations are compatible for validation.

## Contributing

Contributions are welcome! Please open issues or submit pull requests.

## License

This project is licensed under the MIT License.

