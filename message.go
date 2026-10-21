package gotelemetry

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Value decodes a message payload. Invalid types and payload lengths are errors.
func (m TMMsg) Value() (interface{}, error) {
	sizes := [...]int{4, 1, 2, 4, 1, 2, 4}
	if m.Type < TMFloat32 || m.Type > TMString {
		return nil, fmt.Errorf("unknown telemetry type: %d", m.Type)
	}
	if m.Type != TMString && len(m.Payload) != sizes[m.Type] {
		return nil, fmt.Errorf("invalid payload length %d for type %d", len(m.Payload), m.Type)
	}
	switch m.Type {
	case TMFloat32:
		return math.Float32frombits(binary.LittleEndian.Uint32(m.Payload)), nil
	case TMUint8:
		return m.Payload[0], nil
	case TMUint16:
		return binary.LittleEndian.Uint16(m.Payload), nil
	case TMUint32:
		return binary.LittleEndian.Uint32(m.Payload), nil
	case TMInt8:
		return int8(m.Payload[0]), nil
	case TMInt16:
		return int16(binary.LittleEndian.Uint16(m.Payload)), nil
	case TMInt32:
		return int32(binary.LittleEndian.Uint32(m.Payload)), nil
	default:
		return string(m.Payload), nil
	}
}
