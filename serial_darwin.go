//go:build darwin

package gotelemetry

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// darwinBaudRates are the rates termios sets directly; each macOS constant
// equals its rate. Other rates are set with IOSSIOSPEED.
var darwinBaudRates = map[int]uint64{
	50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150, 200: unix.B200,
	300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800, 2400: unix.B2400,
	4800: unix.B4800, 7200: unix.B7200, 9600: unix.B9600, 14400: unix.B14400, 19200: unix.B19200,
	28800: unix.B28800, 38400: unix.B38400, 57600: unix.B57600, 76800: unix.B76800,
	115200: unix.B115200, 230400: unix.B230400,
}

const (
	// IOSSIOSPEED from IOKit/serial/ioss.h, with the 32-bit argument that
	// pyserial and go.bug.st/serial pass.
	iossiospeed = 0x80045402
	// Modem flow control flags of sys/termios.h that x/sys does not define.
	darwinCDTRIflow = 0x00040000
	darwinCDSROflow = 0x00080000
	darwinCCAROflow = 0x00100000
	// select(2) only accepts descriptors below FD_SETSIZE.
	darwinSelectLimit = len(unix.FdSet{}.Bits) * 32
)

// openSerialPort opens a tty without TIOCEXCL, so other processes can still
// open the device. Reads wait in select(2), because macOS poll(2) does not
// support devices; Close interrupts them through a pipe and joins them.
func openSerialPort(name string, options SerialTransportOptions) (*TMTransport, error) {
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	port := &darwinSerialPort{name: name, fd: fd, wake: [2]int{-1, -1}, readTimeout: options.ReadTimeout, writeTimeout: options.WriteTimeout}
	if err := port.configure(options); err != nil {
		_ = port.release()
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return &TMTransport{PortName: name, Read: port.Read, Write: port.Write, Close: port.Close}, nil
}

type darwinSerialPort struct {
	name                      string
	fd                        int
	wake                      [2]int // Close writes to wake[1] to interrupt select.
	readTimeout, writeTimeout time.Duration

	state     sync.RWMutex // Held for reading by operations and for writing by Close.
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func (p *darwinSerialPort) configure(options SerialTransportOptions) error {
	termios, err := unix.IoctlGetTermios(p.fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	special, err := setSerialTermios(termios, options)
	if err != nil {
		return err
	}
	if err := unix.IoctlSetTermios(p.fd, unix.TIOCSETA, termios); err != nil {
		return err
	}
	if special {
		// TIOCSETA resets the rate, so this must come after it.
		if err := unix.IoctlSetPointerInt(p.fd, iossiospeed, options.BaudRate); err != nil {
			return fmt.Errorf("set baud rate %d: %w", options.BaudRate, err)
		}
	}
	if err := unix.Pipe(p.wake[:]); err != nil {
		p.wake = [2]int{-1, -1}
		return err
	}
	for _, fd := range p.wake {
		unix.CloseOnExec(fd)
		if err := unix.SetNonblock(fd, true); err != nil {
			return err
		}
	}
	if p.fd >= darwinSelectLimit || p.wake[0] >= darwinSelectLimit {
		return fmt.Errorf("file descriptor exceeds the select limit of %d", darwinSelectLimit)
	}
	return nil
}

// setSerialTermios applies raw mode without flow control, like the Linux
// backend. It reports whether the rate must be set with IOSSIOSPEED because
// termios cannot express it.
func setSerialTermios(termios *unix.Termios, options SerialTransportOptions) (bool, error) {
	termios.Iflag &^= unix.IXON | unix.IXOFF | unix.IXANY | unix.INPCK | unix.IGNPAR | unix.PARMRK |
		unix.ISTRIP | unix.IGNBRK | unix.BRKINT | unix.INLCR | unix.IGNCR | unix.ICRNL
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHONL |
		unix.ECHOCTL | unix.ECHOPRT | unix.ECHOKE | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB | unix.CRTSCTS |
		darwinCDTRIflow | darwinCDSROflow | darwinCCAROflow | unix.HUPCL
	termios.Cflag |= unix.CREAD | unix.CLOCAL
	termios.Cc[unix.VMIN], termios.Cc[unix.VTIME] = 1, 0

	switch options.DataBits {
	case 5:
		termios.Cflag |= unix.CS5
	case 6:
		termios.Cflag |= unix.CS6
	case 7:
		termios.Cflag |= unix.CS7
	case 8:
		termios.Cflag |= unix.CS8
	default:
		return false, fmt.Errorf("unsupported serial data bits %d", options.DataBits)
	}
	switch options.Parity {
	case NoParity:
	case OddParity:
		termios.Cflag |= unix.PARENB | unix.PARODD
	case EvenParity:
		termios.Cflag |= unix.PARENB
	case MarkParity, SpaceParity:
		return false, fmt.Errorf("serial parity %d is not supported on macOS", options.Parity)
	default:
		return false, fmt.Errorf("unsupported serial parity %d", options.Parity)
	}
	if options.Parity != NoParity {
		termios.Iflag |= unix.INPCK
	}
	switch options.StopBits {
	case OneStopBit:
	case TwoStopBits:
		termios.Cflag |= unix.CSTOPB
	default:
		return false, fmt.Errorf("unsupported serial stop bits %d", options.StopBits)
	}

	if options.BaudRate < 1 || int64(options.BaudRate) > 1<<31-1 {
		return false, fmt.Errorf("unsupported serial baud rate %d", options.BaudRate)
	}
	if rate, ok := darwinBaudRates[options.BaudRate]; ok {
		termios.Ispeed, termios.Ospeed = rate, rate
		return false, nil
	}
	// Keep a valid rate until IOSSIOSPEED sets the requested one.
	termios.Ispeed, termios.Ospeed = unix.B9600, unix.B9600
	return true, nil
}

func (p *darwinSerialPort) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.state.RLock()
	defer p.state.RUnlock()
	if p.closed {
		return 0, p.pathError("read", os.ErrClosed)
	}
	deadline := time.Now().Add(p.readTimeout)
	for {
		ready, err := p.wait(true, deadline)
		if err != nil {
			return 0, p.pathError("read", err)
		}
		if !ready {
			// A silent port is not an error; the caller owns health checks.
			return 0, nil
		}
		n, err := unix.Read(p.fd, b)
		switch {
		case err == unix.EINTR || err == unix.EAGAIN:
			continue
		case err != nil:
			return 0, p.pathError("read", err)
		case n == 0:
			// The device hung up; os.File reports the same on Linux.
			return 0, io.EOF
		}
		return n, nil
	}
}

func (p *darwinSerialPort) Write(b []byte) (int, error) {
	p.state.RLock()
	defer p.state.RUnlock()
	if p.closed {
		return 0, p.pathError("write", os.ErrClosed)
	}
	deadline := time.Now().Add(p.writeTimeout)
	written := 0
	for written < len(b) {
		n, err := unix.Write(p.fd, b[written:])
		if n > 0 {
			written += n
		}
		switch {
		case err == unix.EINTR:
		case err == unix.EAGAIN:
			ready, err := p.wait(false, deadline)
			if err != nil {
				return written, p.pathError("write", err)
			}
			if !ready {
				return written, p.pathError("write", os.ErrDeadlineExceeded)
			}
		case err != nil:
			return written, p.pathError("write", err)
		}
	}
	return written, nil
}

// wait reports whether the port became readable or writable before the
// deadline. Closing the port interrupts it with os.ErrClosed.
func (p *darwinSerialPort) wait(read bool, deadline time.Time) (bool, error) {
	maxFD := p.fd
	if p.wake[0] > maxFD {
		maxFD = p.wake[0]
	}
	for {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		var readable, writable unix.FdSet
		readable.Set(p.wake[0])
		if read {
			readable.Set(p.fd)
		} else {
			writable.Set(p.fd)
		}
		timeout := unix.NsecToTimeval(remaining.Nanoseconds())
		n, err := unix.Select(maxFD+1, &readable, &writable, nil, &timeout)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if readable.IsSet(p.wake[0]) {
			return false, os.ErrClosed
		}
		return n > 0, nil
	}
}

// Close is idempotent. It interrupts pending operations and releases the port
// once they have returned.
func (p *darwinSerialPort) Close() error {
	p.closeOnce.Do(func() {
		_, _ = unix.Write(p.wake[1], []byte{0})
		p.state.Lock()
		defer p.state.Unlock()
		p.closed = true
		if err := p.release(); err != nil {
			p.closeErr = p.pathError("close", err)
		}
	})
	return p.closeErr
}

func (p *darwinSerialPort) release() error {
	err := unix.Close(p.fd)
	for _, fd := range p.wake {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
	return err
}

func (p *darwinSerialPort) pathError(op string, err error) error {
	return &os.PathError{Op: op, Path: p.name, Err: err}
}
