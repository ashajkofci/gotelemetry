//go:build linux

package gotelemetry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

func watchUSBDevices(ctx context.Context) (*usbSourceWatch, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := watcher.Add("/dev"); err != nil {
		watcher.Close()
		return nil, err
	}
	events, failures := make(chan struct{}, 1), make(chan error, 1)
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		defer close(failures)
		for {
			select {
			case <-watchCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				name := filepath.Base(event.Name)
				if (strings.HasPrefix(name, "ttyUSB") || strings.HasPrefix(name, "ttyACM")) &&
					event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
					select {
					case events <- struct{}{}:
					default:
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				select {
				case failures <- err:
				default:
				}
			}
		}
	}()
	return &usbSourceWatch{events: events, errors: failures, close: func() {
		cancel()
		watcher.Close()
		<-done
	}}, nil
}

// listUSBSerialPorts reads the ttys provided by USB devices from sysfs.
func listUSBSerialPorts() ([]usbSerialPort, error) {
	entries, err := os.ReadDir("/sys/class/tty")
	if err != nil {
		return nil, err
	}
	var ports []usbSerialPort
	for _, entry := range entries {
		// Virtual terminals have no device, and ports may vanish while listing.
		path, err := filepath.EvalSymlinks(filepath.Join("/sys/class/tty", entry.Name(), "device"))
		if err != nil {
			continue
		}
		usbPath := usbDeviceDir(path)
		if usbPath == "" {
			continue
		}
		read := func(name string) string {
			value, _ := os.ReadFile(filepath.Join(usbPath, name))
			return strings.TrimSpace(string(value))
		}
		port := usbSerialPort{
			Name: "/dev/" + entry.Name(), Path: path,
			VendorID: read("idVendor"), ProductID: read("idProduct"), SerialNumber: read("serial"),
			Manufacturer: read("manufacturer"), Product: read("product"),
		}
		// devtmpfs creates the node on attach and udev sets its owner and mode
		// right after; tty I/O only touches atime and mtime.
		var stat unix.Stat_t
		if unix.Stat(port.Name, &stat) == nil {
			port.Attached = time.Unix(stat.Ctim.Unix())
		}
		ports = append(ports, port)
	}
	return ports, nil
}

// usbDeviceDir returns the USB device directory owning a tty interface, or ""
// when the tty does not belong to a USB device.
func usbDeviceDir(path string) string {
	for dir := path; strings.HasPrefix(dir, "/sys/devices/"); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir
		}
	}
	return ""
}
