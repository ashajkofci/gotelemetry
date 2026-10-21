package gotelemetry

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// parseIORegUSBSerialPorts extracts serial ports from the XML property list
// printed by `ioreg -a -r -c IOUSBHostDevice -l`. A port belongs to its
// nearest enclosing USB device, so ports behind a hub are reported once even
// when ioreg also prints the hub's subtree. It is parsed on every platform so
// it can be tested anywhere.
func parseIORegUSBSerialPorts(data []byte) ([]usbSerialPort, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil // ioreg prints nothing without USB devices.
	}
	root, err := parsePlist(data)
	if err != nil {
		return nil, fmt.Errorf("ioreg: %w", err)
	}
	var ports []usbSerialPort
	seen := map[string]bool{}
	var walk func(value interface{}, device map[string]interface{})
	walk = func(value interface{}, device map[string]interface{}) {
		switch value := value.(type) {
		case []interface{}:
			for _, child := range value {
				walk(child, device)
			}
		case map[string]interface{}:
			if isIORegUSBDevice(value) {
				device = value
			}
			if name, ok := value["IOCalloutDevice"].(string); ok && device != nil && !seen[name] {
				seen[name] = true
				ports = append(ports, ioregUSBSerialPort(name, device))
			}
			walk(value["IORegistryEntryChildren"], device)
		}
	}
	walk(root, nil)
	return ports, nil
}

// isIORegUSBDevice tells USB devices from their interfaces, which also carry
// idVendor and idProduct.
func isIORegUSBDevice(entry map[string]interface{}) bool {
	_, vendor := plistUint(entry["idVendor"])
	_, product := plistUint(entry["idProduct"])
	_, isInterface := entry["bInterfaceNumber"]
	return vendor && product && !isInterface
}

func ioregUSBSerialPort(name string, device map[string]interface{}) usbSerialPort {
	text := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := device[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	vendor, _ := plistUint(device["idVendor"])
	product, _ := plistUint(device["idProduct"])
	port := usbSerialPort{
		Name: name, Path: name,
		VendorID: fmt.Sprintf("%04x", vendor), ProductID: fmt.Sprintf("%04x", product),
		SerialNumber: text("USB Serial Number", "kUSBSerialNumberString"),
		Manufacturer: text("USB Vendor Name", "kUSBVendorString"),
		Product:      text("USB Product Name", "kUSBProductString"),
	}
	// Registry entry IDs are never reused, so a reattached device gets a new
	// path although its callout device keeps the same name.
	if id, ok := plistUint(device["IORegistryEntryID"]); ok {
		port.Path = fmt.Sprintf("IORegistryEntryID:%#x", id)
	}
	return port
}

func plistUint(value interface{}) (uint64, bool) {
	switch value := value.(type) {
	case int64:
		return uint64(value), value >= 0
	case uint64:
		return value, true
	}
	return 0, false
}

// parsePlist decodes an XML property list into maps, slices, strings, int64
// or uint64 integers and bools. Other values are kept as their text.
func parsePlist(data []byte) (interface{}, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local != "plist" {
			return decodePlistValue(decoder, start)
		}
	}
}

func decodePlistValue(decoder *xml.Decoder, start xml.StartElement) (interface{}, error) {
	switch start.Name.Local {
	case "dict":
		dict := map[string]interface{}{}
		var key string
		haveKey := false
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			switch token := token.(type) {
			case xml.StartElement:
				if token.Name.Local == "key" {
					if err := decoder.DecodeElement(&key, &token); err != nil {
						return nil, err
					}
					haveKey = true
					continue
				}
				if !haveKey {
					return nil, errors.New("plist dict value without key")
				}
				value, err := decodePlistValue(decoder, token)
				if err != nil {
					return nil, err
				}
				dict[key], haveKey = value, false
			case xml.EndElement:
				return dict, nil
			}
		}
	case "array":
		array := []interface{}{}
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			switch token := token.(type) {
			case xml.StartElement:
				value, err := decodePlistValue(decoder, token)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			case xml.EndElement:
				return array, nil
			}
		}
	case "true", "false":
		return start.Name.Local == "true", decoder.Skip()
	}
	var text string
	if err := decoder.DecodeElement(&text, &start); err != nil {
		return nil, err
	}
	if start.Name.Local == "integer" {
		text = strings.TrimSpace(text)
		if value, err := strconv.ParseInt(text, 10, 64); err == nil {
			return value, nil
		}
		if value, err := strconv.ParseUint(text, 10, 64); err == nil {
			return value, nil
		}
	}
	return text, nil
}
