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
}

func (f *Frame) AppendByte(b byte) {
	if b == SOF || b == EOF || b == ESC {
		f.OutgoingBuffer = append(f.OutgoingBuffer, ESC)
	}
	f.OutgoingBuffer = append(f.OutgoingBuffer, b)
}

func (f *Frame) AppendUint16(value uint16) {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, value)
	for _, b := range buf {
		f.AppendByte(b)
	}
}

func (f *Frame) EndFrame() {
	f.OutgoingBuffer = append(f.OutgoingBuffer, EOF)
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
