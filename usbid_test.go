package gotelemetry

import "testing"

func TestParseWindowsInstanceID(t *testing.T) {
	cases := []struct {
		id            string
		ok, usbDevice bool
		want          windowsInstanceID
		wantSerial    string
	}{
		{
			id: `USB\VID_0403&PID_6015\DK0ABCDE`, ok: true, usbDevice: true,
			want:       windowsInstanceID{Enumerator: "USB", VendorID: "0403", ProductID: "6015", Instance: "DK0ABCDE"},
			wantSerial: "DK0ABCDE",
		},
		{
			// Without a usable serial number Windows generates the instance.
			id: `USB\VID_2341&PID_8036\5&3484486E&0&3`, ok: true, usbDevice: true,
			want: windowsInstanceID{Enumerator: "USB", VendorID: "2341", ProductID: "8036", Instance: "5&3484486E&0&3"},
		},
		{
			id: `USB\VID_2341&PID_8036&MI_00\6&2E5DC5CF&0&0000`, ok: true,
			want: windowsInstanceID{Enumerator: "USB", VendorID: "2341", ProductID: "8036", Interface: "00", Instance: "6&2E5DC5CF&0&0000"},
		},
		{
			id: `FTDIBUS\VID_0403+PID_6015+DK0ABCDEA\0000`, ok: true,
			want: windowsInstanceID{Enumerator: "FTDIBUS", VendorID: "0403", ProductID: "6015", Instance: "0000"},
		},
		{
			id: `usb\vid_0FD9&pid_0080\DL41L2A35542`, ok: true, usbDevice: true,
			want:       windowsInstanceID{Enumerator: "USB", VendorID: "0fd9", ProductID: "0080", Instance: "DL41L2A35542"},
			wantSerial: "DL41L2A35542",
		},
		{
			id: `USB\ROOT_HUB30\4&1E61E7A&0&0`, ok: true,
			want: windowsInstanceID{Enumerator: "USB", Instance: "4&1E61E7A&0&0"},
		},
		{
			id: `BTHENUM\{00001101-0000-1000-8000-00805F9B34FB}_LOCALMFG&0002\7&44E03BB&0&08C8C2726767_C00000000`, ok: true,
			want: windowsInstanceID{Enumerator: "BTHENUM", Instance: "7&44E03BB&0&08C8C2726767_C00000000"},
		},
		{
			id: `USB\VID_403&PID_6015\DK0ABCDE`, ok: true,
			want: windowsInstanceID{Enumerator: "USB", VendorID: "403", ProductID: "6015", Instance: "DK0ABCDE"},
		},
		{id: `USB\VID_0403&PID_6015`},
		{id: `USB\VID_0403&PID_6015\`},
		{id: `USB\VID_0403&PID_6015\A\B`},
		{id: ""},
	}
	for _, c := range cases {
		got, ok := parseWindowsInstanceID(c.id)
		if ok != c.ok || got != c.want {
			t.Errorf("parseWindowsInstanceID(%q) = %+v, %v; want %+v, %v", c.id, got, ok, c.want, c.ok)
		}
		if got.isUSBDevice() != c.usbDevice || got.serialNumber() != c.wantSerial {
			t.Errorf("%q: USB device %v, serial %q; want %v, %q", c.id, got.isUSBDevice(), got.serialNumber(), c.usbDevice, c.wantSerial)
		}
	}
}

func TestUSBDeviceInChain(t *testing.T) {
	cases := []struct {
		name   string
		chain  []string
		ok     bool
		index  int
		vendor string
		serial string
	}{
		{
			name:  "FTDI function driver",
			chain: []string{`FTDIBUS\VID_0403+PID_6015+DK0ABCDEA\0000`, `USB\VID_0403&PID_6015\DK0ABCDE`, `USB\ROOT_HUB30\4&1E61E7A&0&0`},
			ok:    true, index: 1, vendor: "0403", serial: "DK0ABCDE",
		},
		{
			name: "FTDI dual channel",
			chain: []string{`FTDIBUS\VID_0403+PID_6010+FT1234ABB\0000`, `USB\VID_0403&PID_6010&MI_01\7&2A3B4C5D&0&0001`,
				`USB\VID_0403&PID_6010\FT1234AB`, `USB\VID_05E3&PID_0610\5&3484486E&0&8`},
			ok: true, index: 2, vendor: "0403", serial: "FT1234AB",
		},
		{
			name:  "CDC ACM composite interface",
			chain: []string{`USB\VID_2341&PID_8036&MI_00\6&2E5DC5CF&0&0000`, `USB\VID_2341&PID_8036\5&3484486E&0&3`},
			ok:    true, index: 1, vendor: "2341",
		},
		{
			name:  "port on the USB device node",
			chain: []string{`USB\VID_10C4&PID_EA60\0001`, `USB\VID_05E3&PID_0610\5&3484486E&0&8`},
			ok:    true, index: 0, vendor: "10c4", serial: "0001",
		},
		{
			// Captured on Windows 10: the radio is a USB device, the port is not.
			name: "Bluetooth serial link",
			chain: []string{`BTHENUM\{00001101-0000-1000-8000-00805F9B34FB}_LOCALMFG&0002\7&44E03BB&0&08C8C2726767_C00000000`,
				`BTH\MS_RFCOMM\6&38572F24&0&0`, `USB\VID_8087&PID_0032\5&3484486E&0&14`},
		},
		{
			name:  "different identity",
			chain: []string{`USB\VID_1111&PID_2222&MI_00\6&1&0&0000`, `USB\VID_3333&PID_4444\5&1&0&3`},
		},
		{name: "motherboard port", chain: []string{`ACPI\PNP0501\1`, `ACPI\PNP0A08\0`}},
		{name: "empty"},
	}
	for _, c := range cases {
		index, id, ok := usbDeviceInChain(c.chain)
		if ok != c.ok || index != c.index || id.VendorID != c.vendor || id.serialNumber() != c.serial {
			t.Errorf("%s: got %d %+v %v; want %d, vendor %q, serial %q, %v", c.name, index, id, ok, c.index, c.vendor, c.serial, c.ok)
		}
	}
}
