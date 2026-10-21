//go:build windows

package gotelemetry

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DCB bit fields; see the DCB structure in winbase.h.
const (
	dcbBinary         = 1 << 0
	dcbParity         = 1 << 1
	dcbOutxCtsFlow    = 1 << 2
	dcbOutxDsrFlow    = 1 << 3
	dcbDtrControl     = 3 << 4
	dcbDsrSensitivity = 1 << 6
	dcbTXContinueXoff = 1 << 7
	dcbOutX           = 1 << 8
	dcbInX            = 1 << 9
	dcbErrorChar      = 1 << 10
	dcbNull           = 1 << 11
	dcbRtsControl     = 3 << 12
	dcbAbortOnError   = 1 << 14
)

const maxDWORD = 0xFFFFFFFF

// windowsIOGrace bounds operations whose driver ignores COMMTIMEOUTS.
const windowsIOGrace = time.Second

// openSerialPort opens a COM port for overlapped I/O. Windows serial drivers
// only allow one handle per port, whatever the share mode. Close cancels
// pending operations and joins them.
func openSerialPort(name string, options SerialTransportOptions) (*TMTransport, error) {
	path := name
	if !strings.HasPrefix(path, `\\`) {
		// The device namespace prefix is required from COM10 on.
		path = `\\.\` + path
	}
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	handle, err := windows.CreateFile(path16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	port := &windowsSerialPort{name: name, handle: handle, readTimeout: options.ReadTimeout, writeTimeout: options.WriteTimeout}
	if err := port.configure(options); err != nil {
		_ = port.release()
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return &TMTransport{PortName: name, Read: port.Read, Write: port.Write, Close: port.Close}, nil
}

type windowsSerialPort struct {
	name                      string
	handle                    windows.Handle
	closeEvent                windows.Handle // Set by Close to cancel pending operations.
	readTimeout, writeTimeout time.Duration
	read, write               windowsSerialOperation

	state     sync.RWMutex // Held for reading by operations and for writing by Close.
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// windowsSerialOperation holds what the driver keeps using after ReadFile or
// WriteFile returns. It lives in the heap-allocated port, not on a goroutine
// stack that may move while the operation is pending.
type windowsSerialOperation struct {
	mu         sync.Mutex
	event      windows.Handle
	overlapped windows.Overlapped
	done       uint32
	data       []byte
}

func (o *windowsSerialOperation) buffer(size int) []byte {
	if cap(o.data) < size {
		o.data = make([]byte, size)
	}
	return o.data[:size]
}

func (p *windowsSerialPort) configure(options SerialTransportOptions) error {
	var err error
	for _, event := range []*windows.Handle{&p.closeEvent, &p.read.event, &p.write.event} {
		if *event, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
			return err
		}
	}
	dcb := windows.DCB{DCBlength: uint32(unsafe.Sizeof(windows.DCB{}))}
	if err := windows.GetCommState(p.handle, &dcb); err != nil {
		return err
	}
	if err := setSerialDCB(&dcb, options); err != nil {
		return err
	}
	if err := windows.SetCommState(p.handle, &dcb); err != nil {
		return err
	}
	timeouts := serialCommTimeouts(options)
	if err := windows.SetCommTimeouts(p.handle, &timeouts); err != nil {
		return err
	}
	// Clear errors left by a previous user, which could block reads.
	var commErrors uint32
	_ = windows.ClearCommError(p.handle, &commErrors, nil)
	return nil
}

// setSerialDCB applies the options without flow control, with DTR and RTS
// asserted as a Linux tty does on open.
func setSerialDCB(dcb *windows.DCB, options SerialTransportOptions) error {
	if options.BaudRate < 1 || uint64(options.BaudRate) > maxDWORD {
		return fmt.Errorf("unsupported serial baud rate %d", options.BaudRate)
	}
	dcb.BaudRate = uint32(options.BaudRate)
	switch options.DataBits {
	case 5, 6, 7, 8:
		dcb.ByteSize = uint8(options.DataBits)
	default:
		return fmt.Errorf("unsupported serial data bits %d", options.DataBits)
	}
	switch options.Parity {
	case NoParity:
		dcb.Parity = windows.NOPARITY
	case OddParity:
		dcb.Parity = windows.ODDPARITY
	case EvenParity:
		dcb.Parity = windows.EVENPARITY
	case MarkParity:
		dcb.Parity = windows.MARKPARITY
	case SpaceParity:
		dcb.Parity = windows.SPACEPARITY
	default:
		return fmt.Errorf("unsupported serial parity %d", options.Parity)
	}
	switch {
	case options.StopBits == OneStopBit:
		dcb.StopBits = windows.ONESTOPBIT
	case options.StopBits == TwoStopBits && options.DataBits == 5:
		// Windows rejects two stop bits with five data bits; POSIX CSTOPB
		// means 1.5 stop bits in that case.
		dcb.StopBits = windows.ONE5STOPBITS
	case options.StopBits == TwoStopBits:
		dcb.StopBits = windows.TWOSTOPBITS
	default:
		return fmt.Errorf("unsupported serial stop bits %d", options.StopBits)
	}
	dcb.Flags &^= dcbBinary | dcbParity | dcbOutxCtsFlow | dcbOutxDsrFlow | dcbDtrControl | dcbDsrSensitivity |
		dcbTXContinueXoff | dcbOutX | dcbInX | dcbErrorChar | dcbNull | dcbRtsControl | dcbAbortOnError
	dcb.Flags |= dcbBinary | windows.DTR_CONTROL_ENABLE | windows.RTS_CONTROL_ENABLE
	if options.Parity != NoParity {
		dcb.Flags |= dcbParity
	}
	return nil
}

// serialCommTimeouts makes reads return as soon as any byte is available, or
// with nothing after the read timeout, and bounds writes by the write timeout.
func serialCommTimeouts(options SerialTransportOptions) windows.CommTimeouts {
	return windows.CommTimeouts{
		ReadIntervalTimeout:        maxDWORD,
		ReadTotalTimeoutMultiplier: maxDWORD,
		ReadTotalTimeoutConstant:   commTimeoutMilliseconds(options.ReadTimeout),
		WriteTotalTimeoutConstant:  commTimeoutMilliseconds(options.WriteTimeout),
	}
}

// commTimeoutMilliseconds rounds up into the range COMMTIMEOUTS accepts:
// zero disables a timeout and MAXDWORD has a special meaning.
func commTimeoutMilliseconds(d time.Duration) uint32 {
	if d >= (maxDWORD-1)*time.Millisecond {
		return maxDWORD - 1
	}
	ms := (d + time.Millisecond - 1) / time.Millisecond
	if ms < 1 {
		return 1
	}
	return uint32(ms)
}

func (p *windowsSerialPort) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.read.mu.Lock()
	defer p.read.mu.Unlock()
	p.state.RLock()
	defer p.state.RUnlock()
	if p.closed {
		return 0, p.pathError("read", os.ErrClosed)
	}
	buffer := p.read.buffer(len(b))
	n, err := p.transfer(&p.read, func() error {
		return windows.ReadFile(p.handle, buffer, &p.read.done, &p.read.overlapped)
	}, p.readTimeout)
	copy(b, buffer[:n])
	if err != nil {
		return n, p.pathError("read", err)
	}
	// A silent port reads nothing; the caller owns health checks.
	return n, nil
}

func (p *windowsSerialPort) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.write.mu.Lock()
	defer p.write.mu.Unlock()
	p.state.RLock()
	defer p.state.RUnlock()
	if p.closed {
		return 0, p.pathError("write", os.ErrClosed)
	}
	buffer := p.write.buffer(len(b))
	copy(buffer, b)
	n, err := p.transfer(&p.write, func() error {
		return windows.WriteFile(p.handle, buffer, &p.write.done, &p.write.overlapped)
	}, p.writeTimeout)
	if err == nil && n < len(b) {
		err = os.ErrDeadlineExceeded
	}
	if err != nil {
		return n, p.pathError("write", err)
	}
	return n, nil
}

// transfer starts an overlapped operation and waits for it. Closing the port,
// or a driver exceeding its timeout, cancels the operation; it is always
// complete when transfer returns.
func (p *windowsSerialPort) transfer(o *windowsSerialOperation, start func() error, timeout time.Duration) (int, error) {
	o.overlapped = windows.Overlapped{HEvent: o.event}
	o.done = 0
	if err := start(); err != nil && err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	wait := uint64(commTimeoutMilliseconds(timeout)) + uint64(windowsIOGrace/time.Millisecond)
	if wait >= windows.INFINITE {
		wait = windows.INFINITE - 1
	}
	event, waitErr := windows.WaitForMultipleObjects([]windows.Handle{o.event, p.closeEvent}, false, uint32(wait))
	if event != windows.WAIT_OBJECT_0 {
		// Fails with ERROR_NOT_FOUND if the operation already completed.
		_ = windows.CancelIoEx(p.handle, &o.overlapped)
	}
	var n uint32
	err := windows.GetOverlappedResult(p.handle, &o.overlapped, &n, true)
	if err == windows.ERROR_OPERATION_ABORTED && event != windows.WAIT_OBJECT_0 {
		switch {
		case event == windows.WAIT_OBJECT_0+1:
			err = os.ErrClosed
		case waitErr != nil:
			err = waitErr
		default:
			// The driver ignored its timeout; report what was transferred.
			err = nil
		}
	}
	return int(n), err
}

// Close is idempotent. It wakes pending operations, which cancel themselves,
// and releases the port once they have returned.
func (p *windowsSerialPort) Close() error {
	p.closeOnce.Do(func() {
		_ = windows.SetEvent(p.closeEvent)
		p.state.Lock()
		defer p.state.Unlock()
		p.closed = true
		if err := p.release(); err != nil {
			p.closeErr = p.pathError("close", err)
		}
	})
	return p.closeErr
}

func (p *windowsSerialPort) release() error {
	err := windows.CloseHandle(p.handle)
	for _, event := range []windows.Handle{p.closeEvent, p.read.event, p.write.event} {
		if event != 0 {
			_ = windows.CloseHandle(event)
		}
	}
	return err
}

func (p *windowsSerialPort) pathError(op string, err error) error {
	return &os.PathError{Op: op, Path: p.name, Err: err}
}
