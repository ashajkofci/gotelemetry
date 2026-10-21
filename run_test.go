package gotelemetry

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func testFrame(msg TMMsg) []byte {
	data := make([]byte, 2)
	binary.LittleEndian.PutUint16(data, uint16(msg.Type))
	data = append(data, msg.Topic...)
	data = append(data, 0)
	data = append(data, msg.Payload...)
	frame := NewFrame()
	frame.BeginFrame()
	for _, b := range data {
		frame.AppendByte(b)
	}
	frame.AppendUint16(CRC16(data))
	frame.EndFrame()
	return frame.OutgoingBuffer
}

func TestRunReceivesUnchangedMessagesAndClosesOnEOF(t *testing.T) {
	msg := TMMsg{Type: TMUint8, Topic: "steady", Payload: []byte{0}}
	reader := bytes.NewReader(bytes.Repeat(testFrame(msg), 3))
	closed, messages, changes := 0, 0, 0
	telemetry := NewTelemetry(&TMTransport{Read: reader.Read, Close: func() error { closed++; return nil }})
	telemetry.Subscribe("", func(got TMMsg) {
		changes++
		// Both callback APIs can call methods that acquire the telemetry mutex.
		telemetry.Subscribe("other", nil)
		if telemetry.GetValue(got.Topic) != uint8(0) {
			t.Error("callback cannot read its updated value")
		}
	})
	telemetry.SetMessageHandler(func(got TMMsg) {
		messages++
		if !reflect.DeepEqual(got, msg) {
			t.Errorf("message = %#v", got)
		}
		_ = telemetry.GetAvailableTopics()
	})
	if err := telemetry.Run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Run returned %v", err)
	}
	if messages != 3 || changes != 1 || closed != 1 {
		t.Fatalf("messages=%d changes=%d closes=%d", messages, changes, closed)
	}
}

func TestRunCancellationUnblocksRead(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reading := make(chan struct{})
	var closes atomic.Int32
	telemetry := NewTelemetry(&TMTransport{
		Read:  func(p []byte) (int, error) { close(reading); return client.Read(p) },
		Close: func() error { closes.Add(1); return client.Close() },
	})
	done := make(chan error, 1)
	go func() { done <- telemetry.Run(ctx) }()
	<-reading
	if err := telemetry.Run(ctx); err == nil {
		t.Error("second reader was allowed")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || closes.Load() != 1 {
			t.Fatalf("error=%v closes=%d", err, closes.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not close the transport and unblock Read")
	}
}

func TestPublishEscapesTopicAndPayload(t *testing.T) {
	var wire bytes.Buffer
	sender := NewTelemetry(&TMTransport{Write: wire.Write})
	want := TMMsg{Type: TMString, Topic: "topic" + string([]byte{ESC}), Payload: []byte{SOF, EOF, ESC, 0}}
	if err := sender.Publish(want.Topic, want.Type, want.Payload); err != nil {
		t.Fatal(err)
	}
	receiver := NewTelemetry(&TMTransport{Read: wire.Read})
	var got TMMsg
	receiver.SetMessageHandler(func(msg TMMsg) { got = msg })
	if err := receiver.Run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %#v, want %#v", got, want)
	}
}

func TestPublishReportsShortWrite(t *testing.T) {
	telemetry := NewTelemetry(&TMTransport{Write: func([]byte) (int, error) { return 0, nil }})
	if err := telemetry.Publish("test", TMUint8, []byte{1}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}

func TestRunRejectsInvalidFrames(t *testing.T) {
	good := testFrame(TMMsg{Type: TMUint8, Topic: "test", Payload: []byte{1}})
	badCRC := append([]byte(nil), good...)
	badCRC[len(badCRC)-2] ^= 1
	invalidLength := testFrame(TMMsg{Type: TMUint32, Topic: "test", Payload: []byte{1}})
	invalidType := testFrame(TMMsg{Type: 90, Topic: "test", Payload: []byte{1}})
	reader := bytes.NewReader(append(append(badCRC, invalidLength...), invalidType...))
	telemetry := NewTelemetry(&TMTransport{Read: reader.Read})
	telemetry.SetMessageHandler(func(TMMsg) { t.Error("invalid frame counted as a message") })
	_ = telemetry.Run(context.Background())
	if len(telemetry.GetAvailableTopics()) != 0 {
		t.Error("invalid frame updated topics")
	}
}

func TestOpenSerialTransportFailure(t *testing.T) {
	if transport, err := OpenSerialTransport("/nonexistent/gotelemetry/port", SerialTransportOptions{}); err == nil || transport != nil {
		t.Fatalf("transport=%v error=%v", transport, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GetTransportContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := GetTransport("", "", 0); err == nil {
		t.Error("zero retries must fail")
	}
}

func FuzzParseFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{1, 0, 't', 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NewTelemetry(nil).parseFrame(data)
	})
}
