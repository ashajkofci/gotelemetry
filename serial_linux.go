//go:build linux

package gotelemetry

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var linuxBaudRates = map[int]uint32{
	50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150,
	200: unix.B200, 300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800,
	2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200,
	38400: unix.B38400, 57600: unix.B57600, 115200: unix.B115200, 230400: unix.B230400,
	460800: unix.B460800, 500000: unix.B500000, 576000: unix.B576000, 921600: unix.B921600,
	1000000: unix.B1000000, 1152000: unix.B1152000, 1500000: unix.B1500000,
	2000000: unix.B2000000, 2500000: unix.B2500000, 3000000: unix.B3000000,
	3500000: unix.B3500000, 4000000: unix.B4000000,
}

// openSerialPort opens a tty without TIOCEXCL, so other processes can still
// open the device. Reads use the runtime poller: deadlines bound them and
// Close unblocks them.
func openSerialPort(name string, options SerialTransportOptions) (*TMTransport, error) {
	file, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	conn, err := file.SyscallConn()
	if err != nil {
		file.Close()
		return nil, err
	}
	var configErr error
	if err := conn.Control(func(fd uintptr) { configErr = configureSerialTermios(int(fd), options) }); err != nil {
		configErr = err
	}
	if configErr != nil {
		file.Close()
		return nil, configErr
	}
	var once sync.Once
	var closeErr error
	return &TMTransport{
		PortName: name,
		Read: func(p []byte) (int, error) {
			if err := file.SetReadDeadline(time.Now().Add(options.ReadTimeout)); err != nil {
				return 0, err
			}
			n, err := file.Read(p)
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// A silent port is not an error; the caller owns health checks.
				err = nil
			}
			return n, err
		},
		Write: func(p []byte) (int, error) {
			if err := file.SetWriteDeadline(time.Now().Add(options.WriteTimeout)); err != nil {
				return 0, err
			}
			return file.Write(p)
		},
		Close: func() error {
			once.Do(func() { closeErr = file.Close() })
			return closeErr
		},
	}, nil
}

// configureSerialTermios applies raw mode without flow control, matching the
// settings of the previous go-serial backend.
func configureSerialTermios(fd int, options SerialTransportOptions) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	termios.Iflag &^= unix.IXON | unix.IXOFF | unix.IXANY | unix.INPCK | unix.IGNPAR | unix.PARMRK |
		unix.ISTRIP | unix.IGNBRK | unix.BRKINT | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IUCLC
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHONL |
		unix.ECHOCTL | unix.ECHOPRT | unix.ECHOKE | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CMSPAR | unix.CSTOPB |
		unix.CRTSCTS | unix.HUPCL | unix.CBAUD
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
		return fmt.Errorf("unsupported serial data bits %d", options.DataBits)
	}
	switch options.Parity {
	case NoParity:
	case OddParity:
		termios.Cflag |= unix.PARENB | unix.PARODD
	case EvenParity:
		termios.Cflag |= unix.PARENB
	case MarkParity:
		termios.Cflag |= unix.PARENB | unix.PARODD | unix.CMSPAR
	case SpaceParity:
		termios.Cflag |= unix.PARENB | unix.CMSPAR
	default:
		return fmt.Errorf("unsupported serial parity %d", options.Parity)
	}
	if options.Parity != NoParity {
		termios.Iflag |= unix.INPCK
	}
	switch options.StopBits {
	case OneStopBit:
	case TwoStopBits:
		termios.Cflag |= unix.CSTOPB
	default:
		return fmt.Errorf("unsupported serial stop bits %d", options.StopBits)
	}

	request := uint(unix.TCSETS)
	if rate, ok := linuxBaudRates[options.BaudRate]; ok {
		termios.Cflag |= rate
		termios.Ispeed, termios.Ospeed = rate, rate
	} else {
		termios.Cflag |= unix.BOTHER
		termios.Ispeed, termios.Ospeed = uint32(options.BaudRate), uint32(options.BaudRate)
		request = unix.TCSETS2
	}
	return unix.IoctlSetTermios(fd, request, termios)
}
