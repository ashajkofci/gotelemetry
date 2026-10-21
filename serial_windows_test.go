//go:build windows

package gotelemetry

import (
	"math"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSerialDCB(t *testing.T) {
	const dcbFields = 1<<15 - 1
	cases := []struct {
		name                       string
		options                    SerialTransportOptions
		byteSize, parity, stopBits uint8
	}{
		{"115200 8N1", SerialTransportOptions{BaudRate: 115200, DataBits: 8}, 8, windows.NOPARITY, windows.ONESTOPBIT},
		{"7E2", SerialTransportOptions{BaudRate: 9600, DataBits: 7, Parity: EvenParity, StopBits: TwoStopBits}, 7, windows.EVENPARITY, windows.TWOSTOPBITS},
		{"8O1", SerialTransportOptions{BaudRate: 250000, DataBits: 8, Parity: OddParity}, 8, windows.ODDPARITY, windows.ONESTOPBIT},
		{"mark", SerialTransportOptions{BaudRate: 19200, DataBits: 8, Parity: MarkParity}, 8, windows.MARKPARITY, windows.ONESTOPBIT},
		{"space", SerialTransportOptions{BaudRate: 19200, DataBits: 6, Parity: SpaceParity}, 6, windows.SPACEPARITY, windows.ONESTOPBIT},
		{"5 data bits, 1.5 stop bits", SerialTransportOptions{BaudRate: 300, DataBits: 5, StopBits: TwoStopBits}, 5, windows.NOPARITY, windows.ONE5STOPBITS},
	}
	for _, c := range cases {
		// Start from a port left with flow control and every other flag set.
		dcb := windows.DCB{Flags: math.MaxUint32, XonLim: 2048}
		if err := setSerialDCB(&dcb, c.options); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		flags := uint32(dcbBinary | windows.DTR_CONTROL_ENABLE | windows.RTS_CONTROL_ENABLE)
		if c.options.Parity != NoParity {
			flags |= dcbParity
		}
		if dcb.BaudRate != uint32(c.options.BaudRate) || dcb.ByteSize != c.byteSize || dcb.Parity != c.parity ||
			dcb.StopBits != c.stopBits || dcb.Flags&dcbFields != flags || dcb.Flags&^dcbFields != math.MaxUint32&^dcbFields ||
			dcb.XonLim != 2048 {
			t.Errorf("%s: got %+v, want flags %#x", c.name, dcb, flags)
		}
	}
	for _, options := range []SerialTransportOptions{
		{BaudRate: 0, DataBits: 8},
		{BaudRate: -1, DataBits: 8},
		{BaudRate: 9600, DataBits: 9},
		{BaudRate: 9600, DataBits: 8, Parity: Parity(5)},
		{BaudRate: 9600, DataBits: 8, StopBits: StopBits(2)},
	} {
		var dcb windows.DCB
		if err := setSerialDCB(&dcb, options); err == nil {
			t.Errorf("%+v: expected an error", options)
		}
	}
}

func TestSerialCommTimeouts(t *testing.T) {
	got := serialCommTimeouts(SerialTransportOptions{ReadTimeout: 250 * time.Millisecond, WriteTimeout: time.Second})
	want := windows.CommTimeouts{
		ReadIntervalTimeout: maxDWORD, ReadTotalTimeoutMultiplier: maxDWORD, ReadTotalTimeoutConstant: 250,
		WriteTotalTimeoutConstant: 1000,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for timeout, want := range map[time.Duration]uint32{
		0:                            1,
		time.Nanosecond:              1,
		1500 * time.Microsecond:      2,
		time.Minute:                  60000,
		time.Duration(math.MaxInt64): maxDWORD - 1,
	} {
		if got := commTimeoutMilliseconds(timeout); got != want {
			t.Errorf("commTimeoutMilliseconds(%v) = %d, want %d", timeout, got, want)
		}
	}
}

// TestListUSBSerialPortsOnThisMachine lists the real ports; it passes without
// any attached adapter but checks what it finds.
func TestListUSBSerialPortsOnThisMachine(t *testing.T) {
	ports, err := listUSBSerialPorts()
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range ports {
		t.Logf("%+v", port)
		if !strings.HasPrefix(port.Name, "COM") || !isHex4(port.VendorID) || !isHex4(port.ProductID) || port.Path == "" {
			t.Errorf("unexpected port %+v", port)
		}
		if strings.HasPrefix(port.Path, `BTHENUM\`) {
			t.Errorf("Bluetooth port listed as USB: %+v", port)
		}
	}
	t.Logf("%d USB serial ports", len(ports))
}

func TestOpenMissingSerialPort(t *testing.T) {
	_, err := OpenSerialTransport("COM255", SerialTransportOptions{})
	if err == nil || !strings.Contains(err.Error(), "COM255") {
		t.Fatalf("expected an open error naming the port, got %v", err)
	}
}
