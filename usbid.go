package gotelemetry

import "strings"

// windowsInstanceID is a parsed Windows device instance ID, such as
// USB\VID_0403&PID_6015\DK0ABCDE for a USB device, USB\VID_2341&PID_8036&MI_00\6&1A2B3C4D&0&0000
// for one interface of a composite device, or FTDIBUS\VID_0403+PID_6015+DK0ABCDEA\0000
// for the port an FTDI driver creates below its USB device. It is parsed on
// every platform so it can be tested anywhere.
type windowsInstanceID struct {
	Enumerator string
	VendorID   string // Lowercase hexadecimal, like Linux sysfs.
	ProductID  string
	Interface  string // MI_xx of a composite device interface.
	Instance   string
}

func parseWindowsInstanceID(id string) (windowsInstanceID, bool) {
	parts := strings.Split(id, `\`)
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return windowsInstanceID{}, false
	}
	parsed := windowsInstanceID{Enumerator: strings.ToUpper(parts[0]), Instance: parts[2]}
	for _, field := range strings.FieldsFunc(parts[1], func(r rune) bool { return r == '&' || r == '+' }) {
		name, value, ok := strings.Cut(field, "_")
		if !ok {
			continue
		}
		switch strings.ToUpper(name) {
		case "VID":
			parsed.VendorID = strings.ToLower(value)
		case "PID":
			parsed.ProductID = strings.ToLower(value)
		case "MI":
			parsed.Interface = value
		}
	}
	return parsed, true
}

// usbDeviceInChain finds the USB device providing a port, given the instance
// IDs from the port node up towards the root. Every node up to the device must
// carry the same VID and PID, so a Bluetooth port does not match the USB radio
// that its Bluetooth bus hangs off.
func usbDeviceInChain(chain []string) (int, windowsInstanceID, bool) {
	var vendor, product string
	for index, id := range chain {
		parsed, ok := parseWindowsInstanceID(id)
		if !ok || parsed.VendorID == "" || parsed.ProductID == "" {
			break
		}
		if index == 0 {
			vendor, product = parsed.VendorID, parsed.ProductID
		} else if parsed.VendorID != vendor || parsed.ProductID != product {
			break
		}
		if parsed.isUSBDevice() {
			return index, parsed, true
		}
	}
	return 0, windowsInstanceID{}, false
}

// isUSBDevice reports whether the ID names a whole USB device rather than an
// interface or a node created by a function driver.
func (id windowsInstanceID) isUSBDevice() bool {
	return id.Enumerator == "USB" && isHex4(id.VendorID) && isHex4(id.ProductID) && id.Interface == ""
}

// serialNumber returns the USB serial number that Windows uses as the
// instance of a USB device. Devices without a usable serial number get a
// generated instance containing '&' instead.
func (id windowsInstanceID) serialNumber() string {
	if !id.isUSBDevice() || strings.Contains(id.Instance, "&") {
		return ""
	}
	return id.Instance
}

func isHex4(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
