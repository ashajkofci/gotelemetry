package gotelemetry

import (
	"encoding/binary"
)

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

// Frame represents the framing protocol for handling incoming and outgoing data.
type Frame struct {
	// IncomingBuffer stores the bytes of the incoming frame.
	IncomingBuffer []byte
	// OutgoingBuffer stores the bytes of the outgoing frame.
	OutgoingBuffer []byte
	// IncomingState represents the current state of the frame parsing.
	IncomingState FramingState
	// OnFrame is called when a complete frame is received.
	OnFrame func(data []byte)
	// OnError is called when an error occurs during frame parsing.
	OnError func(err error)
}

// NewFrame creates a new Frame instance.
func NewFrame() *Frame {
	return &Frame{
		IncomingBuffer: make([]byte, 0),
		OutgoingBuffer: make([]byte, 0),
		IncomingState:  Idle,
		OnFrame:        nil,
		OnError:        nil,
	}
}

// BeginFrame initializes the outgoing buffer with the Start of Frame (SOF) byte.
func (f *Frame) BeginFrame() {
	f.OutgoingBuffer = []byte{SOF}
}

// AppendByte appends a byte to the outgoing buffer, escaping it if necessary.
func (f *Frame) AppendByte(b byte) {
	if b == SOF || b == EOF || b == ESC {
		f.OutgoingBuffer = append(f.OutgoingBuffer, ESC)
	}
	f.OutgoingBuffer = append(f.OutgoingBuffer, b)
}

// AppendUint16 appends a 16-bit unsigned integer to the outgoing buffer, escaping it if necessary.
func (f *Frame) AppendUint16(value uint16) {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, value)
	for _, b := range buf {
		f.AppendByte(b)
	}
}

// EndFrame appends the End of Frame (EOF) byte to the outgoing buffer.
func (f *Frame) EndFrame() {
	f.OutgoingBuffer = append(f.OutgoingBuffer, EOF)
}

// FeedByte processes an incoming byte and updates the frame state accordingly.
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
