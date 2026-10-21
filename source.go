package gotelemetry

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

// USBDevice identifies one enumerated USB serial port: a tty on Linux, a COM
// port on Windows or a callout device (/dev/cu.*) on macOS. Path is a sysfs
// path, a device instance ID or an I/O Registry entry ID; it distinguishes
// device nodes reused after unplugging, except on Windows where the instance
// ID stays the same. SerialNumber permits port renumbering; Windows reports it
// upper-cased.
type USBDevice struct {
	Name         string
	SerialNumber string
	Product      string // USB product string descriptor.
	Path         string
	Attached     time.Time // When the device appeared; zero if unknown.
}

func (d USBDevice) same(other USBDevice) bool {
	return d.Name == other.Name && d.SerialNumber == other.SerialNumber && d.Path == other.Path
}

// latestUSBDevice returns the most recently attached device. Devices without an
// attachment time are ordered by name so the choice stays stable.
func latestUSBDevice(devices []USBDevice) USBDevice {
	latest := devices[0]
	for _, device := range devices[1:] {
		if device.Attached.After(latest.Attached) ||
			(device.Attached.Equal(latest.Attached) && device.Name > latest.Name) {
			latest = device
		}
	}
	return latest
}

// USBTelemetrySourceOptions describes an accessory without imposing a protocol
// handshake, project name, USB identity, or application status representation.
type USBTelemetrySourceOptions struct {
	Name                  string
	VendorID              string
	ProductID             string
	SerialNumber          string // Optional selector when adapters share VID/PID.
	Product               string // Optional USB product string selector; a trailing % matches a prefix.
	Serial                SerialTransportOptions
	ConnectionTimeout     time.Duration // Includes opening and identifying.
	ReconnectInitialDelay time.Duration
	ReconnectMaxDelay     time.Duration

	// Handshake, when non-nil, is published after starting the session reader.
	Handshake *TMMsg
	// Identify returns true once this is the expected accessory, false while
	// waiting for more descriptors, or an error to reject it. If nil, the first
	// valid telemetry message establishes connectivity.
	Identify       func(TMMsg) (bool, error)
	OnConnected    func(USBDevice)
	OnDisconnected func(error) // Also called at startup (nil) and shutdown.
	OnMessage      func(TMMsg) // Includes unchanged values and handshake metadata.
}

// USBTelemetrySource watches USB serial devices and owns a single telemetry
// session. Start performs one discovery scan; a healthy watcher then wakes it
// on device changes. After errors or disconnection it also retries indefinitely
// with capped exponential backoff. When several devices match, the most
// recently attached one is used. Stop cancels and joins all its work.
//
// Options are copied at construction. Callbacks run synchronously on the
// supervisor; return promptly and do not call lifecycle methods from them.
type USBTelemetrySource struct {
	options USBTelemetrySourceOptions
	backend usbSourceBackend
	clock   usbSourceClock
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewUSBTelemetrySource(options USBTelemetrySourceOptions) *USBTelemetrySource {
	if options.Name == "" {
		options.Name = "USB telemetry"
	}
	if options.Serial.BaudRate == 0 {
		options.Serial.BaudRate = 9600
	}
	if options.ConnectionTimeout == 0 {
		options.ConnectionTimeout = 8 * time.Second
	}
	if options.ReconnectInitialDelay == 0 {
		options.ReconnectInitialDelay = time.Second
	}
	if options.ReconnectMaxDelay == 0 {
		options.ReconnectMaxDelay = 30 * time.Second
	}
	if options.Handshake != nil {
		handshake := *options.Handshake
		handshake.Payload = append([]byte(nil), handshake.Payload...)
		options.Handshake = &handshake
	}
	return &USBTelemetrySource{options: options, backend: newUSBSourceBackend(options), clock: usbSourceRealClock{}}
}

func (s *USBTelemetrySource) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	if s.options.VendorID == "" || s.options.ProductID == "" || s.options.Serial.BaudRate < 1 ||
		s.options.ConnectionTimeout <= 0 || s.options.ReconnectInitialDelay <= 0 ||
		s.options.ReconnectMaxDelay < s.options.ReconnectInitialDelay {
		return errors.New("USB identity, baud rate and timeouts must be valid")
	}
	if s.options.Handshake != nil {
		if _, err := s.options.Handshake.Value(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	go func() {
		defer close(done)
		s.supervise(ctx)
	}()
	return nil
}

func (s *USBTelemetrySource) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel, s.done = nil, nil
	}
	return nil
}

func (s *USBTelemetrySource) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

func (s *USBTelemetrySource) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}

func (s *USBTelemetrySource) disconnected(err error) {
	if s.options.OnDisconnected != nil {
		s.options.OnDisconnected(err)
	}
}
func (s *USBTelemetrySource) message(msg TMMsg) {
	if s.options.OnMessage != nil {
		s.options.OnMessage(msg)
	}
}

type usbSourceWatch struct {
	events <-chan struct{}
	errors <-chan error
	close  func()
}

type usbSourceSession struct {
	device   USBDevice
	messages <-chan TMMsg
	done     <-chan error
	close    func()
}

type usbSourceBackend struct {
	watch    func(context.Context) (*usbSourceWatch, error)
	discover func(context.Context) ([]USBDevice, error)
	open     func(context.Context, USBDevice) (*usbSourceSession, error)
}

type usbSourceTimer interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop()
}
type usbSourceClock interface {
	NewTimer(time.Duration) usbSourceTimer
}
type usbSourceRealClock struct{}
type usbSourceRealTimer struct{ *time.Timer }

func (usbSourceRealClock) NewTimer(d time.Duration) usbSourceTimer {
	return usbSourceRealTimer{time.NewTimer(d)}
}
func (t usbSourceRealTimer) C() <-chan time.Time { return t.Timer.C }
func (t usbSourceRealTimer) Stop() {
	if !t.Timer.Stop() {
		select {
		case <-t.Timer.C:
		default:
		}
	}
}
func (t usbSourceRealTimer) Reset(d time.Duration) { t.Stop(); t.Timer.Reset(d) }

type usbSourceAttemptResult struct {
	session *usbSourceSession
	err     error
}

type usbSourceAttempt struct {
	device USBDevice
	cancel context.CancelFunc
	done   chan usbSourceAttemptResult
}

// All connection state belongs to this goroutine. Readers only enqueue messages,
// so a canceled session can never publish status after its replacement starts.
type usbSourceSupervisor struct {
	source  *USBTelemetrySource
	ctx     context.Context
	state   string
	reason  string
	watch   *usbSourceWatch
	attempt *usbSourceAttempt
	session *usbSourceSession

	watchEvents <-chan struct{}
	watchErrors <-chan error
	attemptDone <-chan usbSourceAttemptResult
	messages    <-chan TMMsg
	sessionDone <-chan error
	retry       usbSourceTimer
	deadline    usbSourceTimer
	retryC      <-chan time.Time
	deadlineC   <-chan time.Time
	nextDelay   time.Duration
	pending     []TMMsg
}

func (s *USBTelemetrySource) supervise(ctx context.Context) {
	m := &usbSourceSupervisor{source: s, ctx: ctx, state: "waiting"}
	m.retry = s.clock.NewTimer(time.Hour)
	m.deadline = s.clock.NewTimer(time.Hour)
	m.retry.Stop()
	m.deadline.Stop()
	m.nextDelay = s.options.ReconnectInitialDelay
	defer func() {
		m.retry.Stop()
		m.deadline.Stop()
		m.closeWatch()
		m.closeConnection()
		s.disconnected(context.Canceled)
	}()
	s.disconnected(nil)
	m.ensureWatch()
	m.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-m.watchEvents:
			if !ok {
				m.watchFailed(errors.New("device watcher closed"))
			} else {
				m.reconcile()
			}
		case err, ok := <-m.watchErrors:
			if !ok || err == nil {
				err = errors.New("device watcher closed")
			}
			m.watchFailed(err)
		case result := <-m.attemptDone:
			cancel := m.attempt.cancel
			m.attempt, m.attemptDone = nil, nil
			if result.err != nil {
				cancel()
				if result.session != nil {
					result.session.close()
				}
				m.disconnect(result.err)
			} else {
				closeSession := result.session.close
				result.session.close = func() {
					cancel()
					closeSession()
				}
				m.session = result.session
				m.messages, m.sessionDone = result.session.messages, result.session.done
			}
		case message := <-m.messages:
			m.receive(message)
		case err := <-m.sessionDone:
			if err == nil {
				err = errors.New("telemetry reader stopped")
			}
			m.disconnect(err)
		case <-m.deadlineC:
			m.disconnect(errors.New("telemetry or handshake timeout"))
		case <-m.retryC:
			m.retryC = nil
			m.ensureWatch()
			if m.session == nil && m.attempt == nil {
				m.reconcile()
			}
			m.scheduleRetry()
		}
	}
}

func (m *usbSourceSupervisor) transition(state string, err error) {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if m.state != state || m.reason != reason {
		log.Printf("%s %s: %s", m.source.options.Name, state, reason)
	}
	m.state, m.reason = state, reason
}

func (m *usbSourceSupervisor) closeWatch() {
	if m.watch != nil {
		m.watch.close()
	}
	m.watch, m.watchEvents, m.watchErrors = nil, nil, nil
}

func (m *usbSourceSupervisor) ensureWatch() {
	if m.watch != nil || m.ctx.Err() != nil {
		return
	}
	// Without device events, disconnected sources fall back to polling.
	watch, err := m.source.backend.watch(m.ctx)
	if err != nil {
		return
	}
	m.watch = watch
	m.watchEvents, m.watchErrors = watch.events, watch.errors
}

func (m *usbSourceSupervisor) watchFailed(err error) {
	log.Printf("%s device watcher: %v", m.source.options.Name, err)
	m.closeWatch()
	// An overflow requires reconciliation, not an assumption about connectivity.
	if m.state == "waiting" {
		m.transition("recovering", err)
	}
	m.reconcile()
	m.scheduleRetry()
}

func (m *usbSourceSupervisor) closeConnection() {
	m.messages, m.sessionDone, m.attemptDone, m.deadlineC = nil, nil, nil, nil
	m.deadline.Stop()
	if m.attempt != nil {
		m.attempt.cancel()
		result := <-m.attempt.done
		if result.session != nil {
			result.session.close()
		}
		m.attempt = nil
	}
	if m.session != nil {
		m.session.close()
		m.session = nil
	}
	m.pending = nil
}

func (m *usbSourceSupervisor) disconnect(err error) {
	m.closeConnection()
	m.source.disconnected(err)
	m.transition("recovering", err)
	m.scheduleRetry()
}

// scheduleRetry polls only while disconnected; a connected session is watched
// by its health deadline instead.
func (m *usbSourceSupervisor) scheduleRetry() {
	if m.state == "connecting" || m.state == "connected" || (m.watch != nil && m.state != "recovering") {
		m.retry.Stop()
		m.retryC = nil
		return
	}
	if m.retryC != nil || m.ctx.Err() != nil {
		return
	}
	maximum := m.source.options.ReconnectMaxDelay
	m.retry.Reset(m.nextDelay)
	m.retryC = m.retry.C()
	if m.nextDelay >= maximum/2 {
		m.nextDelay = maximum
	} else {
		m.nextDelay *= 2
	}
}

func matchesUSBProduct(product, selector string) bool {
	product, selector = strings.TrimSpace(product), strings.TrimSpace(selector)
	if selector == "" {
		return true
	}
	if prefix, ok := strings.CutSuffix(selector, "%"); ok {
		return strings.HasPrefix(strings.ToLower(product), strings.ToLower(prefix))
	}
	return strings.EqualFold(product, selector)
}

func (m *usbSourceSupervisor) reconcile() {
	if m.ctx.Err() != nil {
		return
	}
	devices, err := m.source.backend.discover(m.ctx)
	if err != nil {
		if m.session == nil && m.attempt == nil {
			m.transition("recovering", err)
		}
		m.scheduleRetry()
		return
	}
	serial, product := m.source.options.SerialNumber, m.source.options.Product
	candidates := make([]USBDevice, 0, len(devices))
	for _, device := range devices {
		if serial != "" && device.SerialNumber != serial {
			continue
		}
		if !matchesUSBProduct(device.Product, product) {
			continue
		}
		candidates = append(candidates, device)
	}
	var current *USBDevice
	if m.session != nil {
		current = &m.session.device
	} else if m.attempt != nil {
		current = &m.attempt.device
	}
	if current != nil {
		for _, device := range candidates {
			if device.same(*current) {
				return
			}
		}
		m.disconnect(errors.New("USB device removed or replaced"))
	}
	if len(candidates) == 0 {
		m.scheduleRetry()
		return
	}
	chosen := latestUSBDevice(candidates)
	m.transition("connecting", nil)
	m.retry.Stop()
	m.retryC = nil
	m.deadline.Reset(m.source.options.ConnectionTimeout)
	m.deadlineC = m.deadline.C()
	ctx, cancel := context.WithCancel(m.ctx)
	attempt := &usbSourceAttempt{device: chosen, cancel: cancel, done: make(chan usbSourceAttemptResult, 1)}
	m.attempt, m.attemptDone = attempt, attempt.done
	go func() {
		session, err := m.source.backend.open(ctx, chosen)
		attempt.done <- usbSourceAttemptResult{session: session, err: err}
	}()
}

func (m *usbSourceSupervisor) receive(message TMMsg) {
	if m.ctx.Err() != nil {
		return
	}
	if m.state == "connecting" {
		// Retain handshake metadata until the caller confirms device identity.
		if len(m.pending) == 64 {
			m.pending = m.pending[1:]
		}
		m.pending = append(m.pending, message)
		if identify := m.source.options.Identify; identify != nil {
			accepted, err := identify(message)
			if err != nil {
				m.disconnect(err)
				return
			}
			if !accepted {
				return
			}
		}
		m.transition("connected", nil)
		log.Printf("%s connected on %s (USB serial %s)", m.source.options.Name, m.session.device.Name, m.session.device.SerialNumber)
		if connected := m.source.options.OnConnected; connected != nil {
			connected(m.session.device)
		}
		for _, pending := range m.pending {
			m.source.message(pending)
		}
		m.pending = nil
		m.nextDelay = m.source.options.ReconnectInitialDelay
		m.scheduleRetry()
	} else {
		m.source.message(message)
	}
	m.deadline.Reset(m.source.options.ConnectionTimeout)
	m.deadlineC = m.deadline.C()
}
