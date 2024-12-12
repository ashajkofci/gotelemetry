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

	"github.com/tarm/serial"
	"path/to/telemetry"
)

func main() {
	// Configure the serial port
	serialConfig := &serial.Config{
		Name: "COM5",
		Baud: 115200,
		ReadTimeout: time.Millisecond * 500,
	}

	port, err := serial.OpenPort(serialConfig)
	if err != nil {
		log.Fatalf("Failed to open serial port: %v", err)
	}

	transport := &telemetry.TMTransport{
		Read:  port.Read,
		Write: port.Write,
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
	tele.UpdateTelemetry()

	select {} // Keep the program running
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

- `UpdateTelemetry()`:
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
