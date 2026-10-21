//go:build darwin

package gotelemetry

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// fsnotify's kqueue backend opens every entry of a watched directory, which on
// /dev would open serial ports and block on dial-in tty nodes. Sources poll
// instead while disconnected.
func watchUSBDevices(context.Context) (*usbSourceWatch, error) {
	return nil, errUSBDeviceEventsUnavailable
}

// listUSBSerialPorts reads USB devices and their callout devices (/dev/cu.*)
// from the I/O Registry.
func listUSBSerialPorts() ([]usbSerialPort, error) {
	output, err := exec.Command("/usr/sbin/ioreg", "-a", "-r", "-c", "IOUSBHostDevice", "-l").Output()
	if err != nil {
		return nil, fmt.Errorf("ioreg: %w", err)
	}
	ports, err := parseIORegUSBSerialPorts(output)
	if err != nil {
		return nil, err
	}
	for i := range ports {
		// devfs creates the node on attach. Unlike Linux, writes can also
		// refresh its change time, so this orders devices approximately.
		var stat unix.Stat_t
		if unix.Stat(ports[i].Name, &stat) == nil {
			ports[i].Attached = time.Unix(stat.Ctim.Unix())
		}
	}
	return ports, nil
}
