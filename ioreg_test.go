package gotelemetry

import (
	"os"
	"reflect"
	"testing"
)

func TestParseIORegUSBSerialPorts(t *testing.T) {
	data, err := os.ReadFile("testdata/ioreg-usb-serial.plist")
	if err != nil {
		t.Fatal(err)
	}
	ports, err := parseIORegUSBSerialPorts(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []usbSerialPort{
		{
			// Reported once, for the adapter rather than the hub above it.
			Name: "/dev/cu.usbserial-DK0ABCDE", Path: "IORegistryEntryID:0x100000b0b",
			VendorID: "0403", ProductID: "6015", SerialNumber: "DK0ABCDE", Manufacturer: "FTDI", Product: "BAC.2360.02",
		},
		{
			// Fallback keys, no serial number and no registry entry ID.
			Name: "/dev/cu.usbmodem14201", Path: "/dev/cu.usbmodem14201",
			VendorID: "2341", ProductID: "8036", Manufacturer: "Arduino LLC", Product: "Arduino Leonardo",
		},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports:\n got %+v\nwant %+v", ports, want)
	}
}

func TestParseIORegEdgeCases(t *testing.T) {
	ports, err := parseIORegUSBSerialPorts([]byte("\n"))
	if err != nil || ports != nil {
		t.Fatalf("empty output: %v, %v", ports, err)
	}
	for _, data := range []string{
		`<plist><array><dict><key>idVendor</key></array></plist>`,
		`<plist><dict><integer>1</integer></dict></plist>`,
		`not xml`,
	} {
		if _, err := parseIORegUSBSerialPorts([]byte(data)); err == nil {
			t.Errorf("%q: expected an error", data)
		}
	}
	// A single root dict is accepted as well as an array of them.
	ports, err = parseIORegUSBSerialPorts([]byte(`<plist version="1.0"><dict>
		<key>idVendor</key><integer>1027</integer><key>idProduct</key><integer>24597</integer>
		<key>IORegistryEntryChildren</key><array><dict><key>IOCalloutDevice</key><string>/dev/cu.x</string></dict></array>
	</dict></plist>`))
	if err != nil || len(ports) != 1 || ports[0].Name != "/dev/cu.x" || ports[0].VendorID != "0403" {
		t.Fatalf("single root: %+v, %v", ports, err)
	}
}

func TestParsePlistValues(t *testing.T) {
	value, err := parsePlist([]byte(`<?xml version="1.0"?><plist version="1.0"><dict>
		<key>negative</key><integer>-5</integer>
		<key>large</key><integer>18446744073709551000</integer>
		<key>yes</key><true/>
		<key>no</key><false/>
		<key>text</key><string>a &amp; b</string>
		<key>list</key><array><integer>1</integer><string>two</string></array>
	</dict></plist>`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"negative": int64(-5), "large": uint64(18446744073709551000), "yes": true, "no": false,
		"text": "a & b", "list": []interface{}{int64(1), "two"},
	}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("got %#v", value)
	}
	if _, ok := plistUint(int64(-5)); ok {
		t.Fatal("negative integers are not identifiers")
	}
}
