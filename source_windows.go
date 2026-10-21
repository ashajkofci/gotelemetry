//go:build windows

package gotelemetry

import (
	"context"
	"encoding/binary"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Device notifications are not implemented on Windows; sources poll instead
// while disconnected.
func watchUSBDevices(context.Context) (*usbSourceWatch, error) {
	return nil, errUSBDeviceEventsUnavailable
}

var (
	procSetupDiGetDevicePropertyW = windows.NewLazySystemDLL("setupapi.dll").NewProc("SetupDiGetDevicePropertyW")

	portsClassGUID = windows.GUID{Data1: 0x4d36e978, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}
	modemClassGUID = windows.GUID{Data1: 0x4d36e96d, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}

	devpkeyDeviceParent = windows.DEVPROPKEY{
		FmtID: windows.DEVPROPGUID{Data1: 0x4340a6c5, Data2: 0x93fa, Data3: 0x4706, Data4: [8]byte{0x97, 0x2c, 0x7b, 0x64, 0x80, 0x08, 0xa5, 0xa7}},
		PID:   8,
	}
	// The iProduct string descriptor, as the device reported it.
	devpkeyDeviceBusReportedDeviceDesc = windows.DEVPROPKEY{
		FmtID: windows.DEVPROPGUID{Data1: 0x540b947e, Data2: 0x8b40, Data3: 0x45bc, Data4: [8]byte{0xa8, 0xa2, 0x6a, 0x0b, 0x89, 0x4c, 0xbd, 0xa2}},
		PID:   4,
	}
	devpkeyDeviceLastArrivalDate = windows.DEVPROPKEY{
		FmtID: windows.DEVPROPGUID{Data1: 0x83da6326, Data2: 0x97a6, Data3: 0x4088, Data4: [8]byte{0x94, 0x53, 0xa1, 0x92, 0x3f, 0x57, 0x3b, 0x29}},
		PID:   102,
	}
)

// listUSBSerialPorts lists the COM ports of present USB devices. A port node
// may be the USB device itself, an interface of a composite device, or a node
// created by a function driver such as FTDIBUS; the USB device is found by
// walking up the device tree. Windows reports the serial number upper-cased in
// the device instance ID, and no USB manufacturer string.
func listUSBSerialPorts() ([]usbSerialPort, error) {
	devices, err := windows.SetupDiGetClassDevsEx(nil, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, err
	}
	defer devices.Close()
	byID := map[string]*windows.DevInfoData{}
	var serialPorts []*windows.DevInfoData
	for index := 0; ; index++ {
		data, err := windows.SetupDiEnumDeviceInfo(devices, index)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}
		id, err := windows.SetupDiGetDeviceInstanceId(devices, data)
		if err != nil {
			continue
		}
		byID[strings.ToUpper(id)] = data
		if data.ClassGUID == portsClassGUID || data.ClassGUID == modemClassGUID {
			serialPorts = append(serialPorts, data)
		}
	}
	var ports []usbSerialPort
	for _, data := range serialPorts {
		name := comPortName(devices, data)
		if name == "" {
			continue
		}
		portID, err := windows.SetupDiGetDeviceInstanceId(devices, data)
		if err != nil {
			continue
		}
		usbDevice, usbID, ok := findUSBDevice(devices, data, portID, byID)
		if !ok {
			continue // Not a USB port, e.g. a Bluetooth or motherboard port.
		}
		port := usbSerialPort{
			Name: name, Path: portID,
			VendorID: usbID.VendorID, ProductID: usbID.ProductID, SerialNumber: usbID.serialNumber(),
		}
		port.Product, _ = deviceStringProperty(devices, usbDevice, &devpkeyDeviceBusReportedDeviceDesc)
		if arrival, err := deviceTimeProperty(devices, data, &devpkeyDeviceLastArrivalDate); err == nil {
			port.Attached = arrival
		}
		ports = append(ports, port)
	}
	return ports, nil
}

// findUSBDevice returns the USB device providing a port node.
func findUSBDevice(devices windows.DevInfo, data *windows.DevInfoData, id string, byID map[string]*windows.DevInfoData) (*windows.DevInfoData, windowsInstanceID, bool) {
	chain, nodes := []string{id}, []*windows.DevInfoData{data}
	for len(chain) < 4 {
		parent, err := deviceStringProperty(devices, nodes[len(nodes)-1], &devpkeyDeviceParent)
		next := byID[strings.ToUpper(parent)]
		if err != nil || next == nil {
			break
		}
		chain, nodes = append(chain, parent), append(nodes, next)
	}
	index, usbID, ok := usbDeviceInChain(chain)
	if !ok {
		return nil, windowsInstanceID{}, false
	}
	return nodes[index], usbID, true
}

// comPortName returns the COM port name a Ports or Modem class device was
// assigned, or "" for other devices such as parallel ports.
func comPortName(devices windows.DevInfo, data *windows.DevInfoData) string {
	handle, err := windows.SetupDiOpenDevRegKey(devices, data, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DEV, windows.KEY_READ)
	if err != nil {
		return ""
	}
	key := registry.Key(handle)
	defer key.Close()
	name, _, err := key.GetStringValue("PortName")
	if err != nil || !strings.HasPrefix(strings.ToUpper(name), "COM") {
		return ""
	}
	return name
}

// deviceProperty reads a unified device property. x/sys only decodes string
// properties, and the arrival date is a FILETIME.
func deviceProperty(devices windows.DevInfo, data *windows.DevInfoData, key *windows.DEVPROPKEY) (windows.DEVPROPTYPE, []byte, error) {
	buffer := make([]byte, 256)
	for {
		var kind windows.DEVPROPTYPE
		var size uint32
		ok, _, err := procSetupDiGetDevicePropertyW.Call(uintptr(devices), uintptr(unsafe.Pointer(data)),
			uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)), uintptr(unsafe.Pointer(&size)), 0)
		if ok != 0 {
			return kind, buffer[:size], nil
		}
		if err == windows.ERROR_INSUFFICIENT_BUFFER && int(size) > len(buffer) {
			buffer = make([]byte, size)
			continue
		}
		return 0, nil, err
	}
}

func deviceStringProperty(devices windows.DevInfo, data *windows.DevInfoData, key *windows.DEVPROPKEY) (string, error) {
	kind, value, err := deviceProperty(devices, data, key)
	if err != nil {
		return "", err
	}
	if kind != windows.DEVPROP_TYPE_STRING {
		return "", windows.ERROR_INVALID_DATA
	}
	text := make([]uint16, len(value)/2)
	for i := range text {
		text[i] = binary.LittleEndian.Uint16(value[2*i:])
	}
	return strings.TrimSpace(windows.UTF16ToString(text)), nil
}

func deviceTimeProperty(devices windows.DevInfo, data *windows.DevInfoData, key *windows.DEVPROPKEY) (time.Time, error) {
	kind, value, err := deviceProperty(devices, data, key)
	if err != nil {
		return time.Time{}, err
	}
	if kind != windows.DEVPROP_TYPE_FILETIME || len(value) != 8 {
		return time.Time{}, windows.ERROR_INVALID_DATA
	}
	filetime := windows.Filetime{LowDateTime: binary.LittleEndian.Uint32(value), HighDateTime: binary.LittleEndian.Uint32(value[4:])}
	return time.Unix(0, filetime.Nanoseconds()), nil
}
