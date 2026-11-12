# Telemetry Library in Go

This repository provides a Go implementation of a telemetry system based on framing, CRC validation, and topic-based message handling. It is compatible with the [Overdrivr/Telemetry](https://github.com/Overdrivr/Telemetry) protocol, making it interoperable with Python implementations. It is designed for serial communication and supports attaching variables, subscribing to topics, and publishing data.

## Features

- **CRC16 Validation**: Ensures message integrity with a 16-bit cyclic redundancy check.
- **Framing Protocol**: Supports custom start-of-frame (SOF), end-of-frame (EOF), and escape sequences.
- **Topic-Based Messaging**:
  - Attach variables to topics.
  - Subscribe to topics with callbacks.
  - Publish messages to topics.
- **Serial Communication**: Native serial ports and USB discovery on Linux, Windows and macOS, without cgo.

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

### Automatic USB sources

`USBTelemetrySource` owns device discovery, the serial session, health checks,
and recovery. Applications can embed it in an accessory type and supply their
own USB identity, serial settings, handshake, identification, and callbacks:

```go
type Accessory struct {
    *gotelemetry.USBTelemetrySource
}

func NewAccessory(vendorID, productID string, onMessage func(gotelemetry.TMMsg)) *Accessory {
    return &Accessory{gotelemetry.NewUSBTelemetrySource(gotelemetry.USBTelemetrySourceOptions{
        VendorID: vendorID,
        ProductID: productID,
        Serial: gotelemetry.SerialTransportOptions{BaudRate: 115200},
        OnMessage: onMessage,
    })}
}
```

Call `Start()` to begin detection and `Stop()` to cancel and join all work.
Both are idempotent. `IsRunning()` reports whether detection is running,
including while waiting for a device. Separate source instances have independent
sessions. Lifecycle methods must be called outside source callbacks.

On Linux, the source watches `/dev` before its initial scan, then responds to
USB serial device changes without periodic scans while waiting normally.
After a disconnect, failed open, rejected identity, or silent connection, it
also retries indefinitely with exponential delays from one to thirty seconds.
Device events bypass that delay. If device events are unavailable, including
on Windows and macOS, the source polls while no device is connected. A connected
session is supervised by its health deadline and is never polled.

The default connection timeout is eight seconds. Each valid frame resets the
health deadline, including an unchanged value. An optional `Handshake` is sent
after starting the reader; `Identify` can wait for the expected descriptor or
reject the device. Without `Identify`, the first valid message connects it.
`OnConnected` runs before replaying identification messages to `OnMessage`.
`OnDisconnected` also runs at startup with nil and at shutdown with cancellation.
Callbacks are synchronous and should return promptly.

Set `Product` to only open adapters whose USB product string descriptor
matches (case-insensitive). A trailing `%` matches a product prefix, so
`Accessory.%` accepts both `Accessory.02` and `Accessory.03`. Selectors without
`%` continue to match exactly. Set `Products` to a list of alternative selectors
to accept multiple products, for example `[]string{"Accessory.%", "Legacy.02"}`.
A nonempty list overrides `Product`; any matching entry accepts the product.
Blank list entries are rejected by `Start`. The list is copied at construction.
Set `SerialNumber` to select a particular adapter.
When several adapters still match, the most
recently attached one is used. Port renumbering is handled on discovery.
Timeouts and retry delays are configurable in `USBTelemetrySourceOptions`.

### Sessions managed by the application

Use `OpenSerialTransport` to open a selected port once, and `Run(ctx)` to
receive until cancellation or a read error. These APIs do not discover USB
devices or reconnect; the caller owns those policies. Serial parameters are
typed options (9600 baud, 8N1, one-second I/O timeouts by default).

```go
transport, err := gotelemetry.OpenSerialTransport(portName, gotelemetry.SerialTransportOptions{
    BaudRate: 115200,
    ReadTimeout: time.Second,
    WriteTimeout: time.Second,
})
if err != nil {
    return err
}
telemetry := gotelemetry.NewTelemetry(transport)
telemetry.SetMessageHandler(func(msg gotelemetry.TMMsg) {
    value, _ := msg.Value()
    log.Printf("%s: %v", msg.Topic, value)
})
return telemetry.Run(ctx)
```

Import `github.com/ashajkofci/gotelemetry` for this example. The port name and serial
settings belong to the calling application.

`Run` is blocking, returns the transport error or `ctx.Err()`, and closes
its transport on exit. Cancellation closes it and joins pending reads. Serial I/O uses finite timeouts so closure is synchronized with the serial driver.
Serial ports are supported natively, without cgo, on:

- **Linux**: ttys of USB devices are found through sysfs, and watched in `/dev`.
  `OpenSerialTransport` does not lock the port for exclusive use, so other
  processes can still open it.
- **Windows**: COM ports of USB devices are found through SetupAPI, including
  ports created by function drivers such as FTDI's; port names are `COM3` or
  `\\.\COM10`. Windows serial drivers only allow one open handle per port.
  Serial numbers come from device instance IDs, which Windows upper-cases, and
  no manufacturer string is reported.
- **macOS**: callout devices (`/dev/cu.*`) of USB devices are found with
  `ioreg`. The port is not locked for exclusive use. Mark and space parity are
  not supported.

On other platforms `OpenSerialTransport`, `GetTransport` and USB sources return
an unsupported error; the protocol itself works with any custom `TMTransport`.
To check a real port, run
`GOTELEMETRY_TEST_PORT=COM3 go test -run TestSerialTransportHardware`; it only
reads from the port.
Custom transports should supply a `Close` function that unblocks reads, or use
bounded read timeouts. Serial transports returned by `OpenSerialTransport`
have an idempotent `Close`. Use a new telemetry instance after reconnecting;
only one reader may run per instance.

`SetMessageHandler` receives every valid message, including repeated values.
`Subscribe` retains its existing new-or-changed-value behavior: a specific topic
callback takes precedence over the general callback. A new topic notifies even
at zero; an attached, unchanged value does not notify. The message handler runs
after change subscriptions, including when no subscription fires. Both callbacks
run synchronously outside the data mutex and may call `GetValue`,
`GetAvailableTopics`, `Subscribe`, and `Publish`. Return promptly and do not
wait for the reader to exit inside a callback. Retained messages own their payload
bytes. Invalid CRCs, message types, and payload sizes do not invoke handlers.

The existing `GetTransport` and `UpdateTelemetry` APIs remain available.
`GetTransportContext(ctx, options...)` adds cancellation to legacy discovery.
A retry count of one performs one attempt without a sleep. New application
supervisors should use `Run` to avoid competing reconnect policies.

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

- `GetValue(topic string) interface{}`:
  Returns the value of a topic in the hash table.

- `GetAvailableTopics() []string`:
  Returns a list of all received topics.

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

