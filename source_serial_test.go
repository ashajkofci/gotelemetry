package gotelemetry

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestUSBSourceSessionRunsCallerHandshakeAndRepeatedTelemetry(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := NewTelemetry(&TMTransport{Read: peer.Read, Write: peer.Write, Close: peer.Close})
	hello := make(chan TMMsg, 1)
	remote.SetMessageHandler(func(msg TMMsg) { hello <- msg })
	remoteDone := make(chan error, 1)
	go func() { remoteDone <- remote.Run(ctx) }()
	handshake := &TMMsg{Topic: "identify", Type: TMString, Payload: []byte("arbitrary protocol request")}
	session, err := newUSBSourceSession(ctx, USBDevice{Name: "test"}, &TMTransport{
		Read: client.Read, Write: client.Write, Close: client.Close,
	}, handshake)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	select {
	case got := <-hello:
		if !reflect.DeepEqual(got, *handshake) {
			t.Fatalf("handshake=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not publish caller's handshake")
	}
	for i := 0; i < 2; i++ {
		if err := remote.Publish("reading", TMUint8, []byte{0}); err != nil {
			t.Fatal(err)
		}
		select {
		case msg := <-session.messages:
			value, err := msg.Value()
			if msg.Topic != "reading" || value != uint8(0) || err != nil {
				t.Fatalf("message=%+v", msg)
			}
		case <-time.After(time.Second):
			t.Fatal("unchanged message was lost")
		}
	}
	session.close()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("serial session did not stop")
	}
	cancel()
	<-remoteDone
}

func TestUSBSourceIndependentAccessoriesHaveDifferentIdentityPolicies(t *testing.T) {
	first := newUSBSourceFixture(t, usbSourceTestDevice)
	otherDevice := USBDevice{Name: "/dev/ttyACM2", SerialNumber: "sensor-b", Path: "/sys/other"}
	second := newUSBSourceFixture(t, otherDevice)
	second.service.options.Name = "Accessory B"
	second.service.options.VendorID, second.service.options.ProductID = "abcd", "0123"
	second.service.options.Serial.BaudRate = 57600
	second.service.options.Identify = func(msg TMMsg) (bool, error) {
		if msg.Topic != "different/identity" {
			return false, nil
		}
		if string(msg.Payload) != "accessory B" {
			return false, errors.New("wrong B identity")
		}
		return true, nil
	}
	first.start(t)
	second.start(t)
	first.connect(t, 0)
	secondSession := second.session(t, 0)
	secondSession.send("descriptor/project", "Accessory A")
	if second.status.get("connected") == "1" {
		t.Fatal("accessory B used accessory A's identity policy")
	}
	secondSession.send("different/identity", "accessory B")
	usbSourceAwait(t, func() bool { return second.status.get("connected") == "1" })
	first.setDevices()
	first.notify()
	usbSourceAwait(t, func() bool { return first.status.get("connected") == "0" })
	if second.status.get("connected") != "1" || secondSession.closes.Load() != 0 {
		t.Fatal("one source's disconnect changed another accessory")
	}
}
