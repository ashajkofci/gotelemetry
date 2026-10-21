package gotelemetry

import (
	"reflect"
	"testing"
)

// These contracts also run against the original library to check compatibility.
func TestSubscribeContractValueChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    TMType
		zero    []byte
		changed []byte
	}{
		{"float32", TMFloat32, []byte{0, 0, 0, 0}, []byte{0, 0, 128, 63}},
		{"uint8", TMUint8, []byte{0}, []byte{1}},
		{"uint16", TMUint16, []byte{0, 0}, []byte{1, 0}},
		{"uint32", TMUint32, []byte{0, 0, 0, 0}, []byte{1, 0, 0, 0}},
		{"int8", TMInt8, []byte{0}, []byte{255}},
		{"int16", TMInt16, []byte{0, 0}, []byte{255, 255}},
		{"int32", TMInt32, []byte{0, 0, 0, 0}, []byte{255, 255, 255, 255}},
		{"string", TMString, []byte{}, []byte("value")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			telemetry := NewTelemetry(nil)
			calls := 0
			telemetry.Subscribe("value", func(TMMsg) { calls++ })
			for i, payload := range [][]byte{tc.zero, tc.zero, tc.changed, tc.changed, tc.zero} {
				telemetry.TryUpdateHashTable(TMMsg{Type: tc.kind, Topic: "value", Payload: payload})
				want := []int{1, 1, 2, 2, 3}[i]
				if calls != want {
					t.Fatalf("update %d: callbacks=%d, want %d", i, calls, want)
				}
			}
		})
	}
}

func TestSubscribeContractRoutingAndReplacement(t *testing.T) {
	telemetry := NewTelemetry(nil)
	var calls []string
	telemetry.Subscribe("", func(msg TMMsg) { calls = append(calls, "general:"+msg.Topic) })
	telemetry.Subscribe("specific", func(TMMsg) { calls = append(calls, "specific") })
	var attached uint8
	telemetry.Attach("attached", &attached)
	update := func(topic string, value byte) {
		telemetry.TryUpdateHashTable(TMMsg{Type: TMUint8, Topic: topic, Payload: []byte{value}})
	}
	update("specific", 0)
	update("specific", 0)
	update("other", 0)
	update("other", 0)
	update("attached", 0) // An attached, unchanged value has never notified.
	update("attached", 1)
	telemetry.Subscribe("specific", func(TMMsg) { calls = append(calls, "replacement") })
	update("specific", 0) // Replacing a callback does not replay the value.
	update("specific", 1)
	telemetry.Subscribe("", func(msg TMMsg) { calls = append(calls, "new-general:"+msg.Topic) })
	update("other", 1)
	telemetry.Subscribe("", nil)
	update("other", 2)
	update("specific", 2)
	want := []string{"specific", "general:other", "general:attached", "replacement", "new-general:other", "replacement"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("callbacks=%v, want %v", calls, want)
	}
	if attached != 1 {
		t.Fatalf("attached value=%d, want 1", attached)
	}
}

func TestMessageHandlerLeavesSubscriptionsUnchanged(t *testing.T) {
	telemetry := NewTelemetry(nil)
	var calls []string
	telemetry.Subscribe("", func(TMMsg) { calls = append(calls, "general") })
	telemetry.Subscribe("value", func(TMMsg) { calls = append(calls, "specific") })
	telemetry.SetMessageHandler(func(msg TMMsg) {
		calls = append(calls, "message")
		if telemetry.GetValue(msg.Topic) != msg.Payload[0] {
			t.Error("handler ran before updating the topic")
		}
	})
	for _, value := range []byte{0, 0, 1} {
		for _, b := range testFrame(TMMsg{Type: TMUint8, Topic: "value", Payload: []byte{value}}) {
			telemetry.Frame.FeedByte(b)
		}
	}
	want := []string{"specific", "message", "message", "specific", "message"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("callbacks=%v, want %v", calls, want)
	}
}
