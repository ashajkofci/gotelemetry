package gotelemetry

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type usbSourceFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*usbSourceFakeTimer
}

type usbSourceFakeTimer struct {
	clock  *usbSourceFakeClock
	ch     chan time.Time
	at     time.Time
	active bool
}

func newUSBSourceFakeClock() *usbSourceFakeClock {
	return &usbSourceFakeClock{now: time.Unix(1700000000, 0)}
}
func (c *usbSourceFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *usbSourceFakeClock) NewTimer(d time.Duration) usbSourceTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &usbSourceFakeTimer{clock: c, ch: make(chan time.Time, 1), at: c.now.Add(d), active: true}
	c.timers = append(c.timers, timer)
	return timer
}
func (timer *usbSourceFakeTimer) C() <-chan time.Time { return timer.ch }
func (timer *usbSourceFakeTimer) Reset(d time.Duration) {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	select {
	case <-timer.ch:
	default:
	}
	timer.at, timer.active = timer.clock.now.Add(d), true
}
func (timer *usbSourceFakeTimer) Stop() {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	timer.active = false
	select {
	case <-timer.ch:
	default:
	}
}
func (c *usbSourceFakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, timer := range c.timers {
		if timer.active && !timer.at.After(c.now) {
			timer.active = false
			timer.ch <- c.now
		}
	}
}
func (c *usbSourceFakeClock) hasTimer(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, timer := range c.timers {
		if timer.active && timer.at.Equal(c.now.Add(d)) {
			return true
		}
	}
	return false
}
func (c *usbSourceFakeClock) activeTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, timer := range c.timers {
		if timer.active {
			count++
		}
	}
	return count
}

type usbSourceFakeStatus struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *usbSourceFakeStatus) SetKey(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = fmt.Sprint(value)
	return nil
}
func (s *usbSourceFakeStatus) GetKey(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}
func (s *usbSourceFakeStatus) get(key string) string { value, _ := s.GetKey(key); return value }

type usbSourceFakeSession struct {
	messages chan TMMsg
	done     chan error
	closed   chan struct{}
	joined   chan struct{}
	closes   atomic.Int32
	once     sync.Once
	device   USBDevice
}

func newUSBSourceFakeSession(ctx context.Context, device USBDevice) *usbSourceFakeSession {
	s := &usbSourceFakeSession{
		device: device, messages: make(chan TMMsg, 100),
		done: make(chan error, 1), closed: make(chan struct{}), joined: make(chan struct{}),
	}
	go func() {
		defer close(s.joined)
		select {
		case <-ctx.Done():
			s.finish(ctx.Err())
		case <-s.closed:
		}
	}()
	return s
}
func (s *usbSourceFakeSession) finish(err error) {
	s.once.Do(func() { s.closes.Add(1); close(s.closed); s.done <- err })
}
func (s *usbSourceFakeSession) session() *usbSourceSession {
	return &usbSourceSession{device: s.device, messages: s.messages, done: s.done, close: func() {
		s.finish(context.Canceled)
		<-s.joined
	}}
}
func (s *usbSourceFakeSession) send(topic string, value interface{}) {
	msg := TMMsg{Topic: topic}
	switch v := value.(type) {
	case string:
		msg.Type, msg.Payload = TMString, []byte(v)
	case uint32:
		msg.Type, msg.Payload = TMUint32, make([]byte, 4)
		binary.LittleEndian.PutUint32(msg.Payload, v)
	case float32:
		msg.Type, msg.Payload = TMFloat32, make([]byte, 4)
		binary.LittleEndian.PutUint32(msg.Payload, math.Float32bits(v))
	default:
		panic("unsupported fake message")
	}
	s.messages <- msg
}

type usbSourceFixture struct {
	service       *USBTelemetrySource
	clock         *usbSourceFakeClock
	status        *usbSourceFakeStatus
	mu            sync.Mutex
	devices       []USBDevice
	sessions      []*usbSourceFakeSession
	events        chan struct{}
	failures      chan error
	scans         int
	watches       int
	watchCloses   int
	openCalls     int
	watchError    error
	discoverError error
	openError     error
	openFn        func(context.Context, USBDevice) (*usbSourceSession, error)
}

func newUSBSourceFixture(t *testing.T, devices ...USBDevice) *usbSourceFixture {
	t.Helper()
	f := &usbSourceFixture{
		clock: newUSBSourceFakeClock(), status: &usbSourceFakeStatus{values: make(map[string]string)},
		events: make(chan struct{}, 1), failures: make(chan error, 1),
		devices: append([]USBDevice(nil), devices...),
	}
	f.service = NewUSBTelemetrySource(USBTelemetrySourceOptions{
		Name: "Accessory A", VendorID: "1234", ProductID: "5678", Serial: SerialTransportOptions{BaudRate: 38400},
		Identify: func(msg TMMsg) (bool, error) {
			if msg.Topic != "descriptor/project" {
				return false, nil
			}
			if strings.Trim(string(msg.Payload), " \\x00") != "Accessory A" {
				return false, errors.New("wrong project")
			}
			return true, nil
		},
		OnConnected: func(USBDevice) { _ = f.status.SetKey("connected", "1") },
		OnDisconnected: func(error) {
			_ = f.status.SetKey("connected", "0")
			_ = f.status.SetKey("received", "0")
		},
		OnMessage: func(msg TMMsg) {
			value, _ := msg.Value()
			_ = f.status.SetKey(msg.Topic, value)
			_ = f.status.SetKey("received", f.clock.Now().Unix())
			switch msg.Topic {
			case "descriptor/serialnumber":
				_ = f.status.SetKey("serialNumber", value)
			case "descriptor/firmware":
				_ = f.status.SetKey("firmware", value)
			}
		},
	})
	f.service.clock = f.clock
	f.service.backend = usbSourceBackend{
		watch: func(context.Context) (*usbSourceWatch, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.watches++
			if f.watchError != nil {
				return nil, f.watchError
			}
			return &usbSourceWatch{events: f.events, errors: f.failures, close: func() {
				f.mu.Lock()
				f.watchCloses++
				f.mu.Unlock()
			}}, nil
		},
		discover: func(ctx context.Context) ([]USBDevice, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.scans++
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return append([]USBDevice(nil), f.devices...), f.discoverError
		},
		open: func(ctx context.Context, device USBDevice) (*usbSourceSession, error) {
			f.mu.Lock()
			f.openCalls++
			openFn, openErr := f.openFn, f.openError
			f.mu.Unlock()
			if openFn != nil {
				return openFn(ctx, device)
			}
			if openErr != nil {
				return nil, openErr
			}
			session := newUSBSourceFakeSession(ctx, device)
			f.mu.Lock()
			f.sessions = append(f.sessions, session)
			f.mu.Unlock()
			return session.session(), nil
		},
	}
	t.Cleanup(func() {
		_ = f.service.Stop()
		if f.clock.activeTimers() != 0 {
			t.Error("supervisor left active timers")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, session := range f.sessions {
			if session.closes.Load() != 1 {
				t.Error("session was not closed exactly once")
			}
		}
	})
	return f
}
func (f *usbSourceFixture) start(t *testing.T) {
	t.Helper()
	if err := f.service.Start(); err != nil {
		t.Fatal(err)
	}
	usbSourceAwait(t, func() bool { return f.scanCount() > 0 })
}
func (f *usbSourceFixture) setDevices(devices ...USBDevice) {
	f.mu.Lock()
	f.devices = append([]USBDevice(nil), devices...)
	f.mu.Unlock()
}
func (f *usbSourceFixture) notify() {
	select {
	case f.events <- struct{}{}:
	default:
	}
}
func (f *usbSourceFixture) scanCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.scans }
func (f *usbSourceFixture) session(t *testing.T, index int) *usbSourceFakeSession {
	t.Helper()
	usbSourceAwait(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sessions) > index })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[index]
}
func (f *usbSourceFixture) connect(t *testing.T, index int) *usbSourceFakeSession {
	t.Helper()
	session := f.session(t, index)
	session.send("descriptor/project", "Accessory A")
	usbSourceAwait(t, func() bool { return f.status.get("connected") == "1" })
	return session
}
func usbSourceAwait(t *testing.T, ready func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-timeout.C:
			t.Fatal("timed out waiting for USBSource state")
		case <-tick.C:
		}
	}
}

var usbSourceTestDevice = USBDevice{Name: "/dev/ttyUSB4", SerialNumber: "device-a", Path: "/sys/devices/usb/1/ttyUSB4"}

func TestUSBSourceEventDiscoveryDoesNotPollWhileWaiting(t *testing.T) {
	f := newUSBSourceFixture(t)
	f.start(t)
	usbSourceAwait(t, func() bool { return f.clock.activeTimers() == 0 })
	f.clock.advance(24 * time.Hour)
	if f.scanCount() != 1 || !f.service.IsRunning() {
		t.Fatal("healthy waiting should only scan once")
	}
	f.setDevices(usbSourceTestDevice)
	f.notify()
	first := f.connect(t, 0)
	f.service.Start()
	if first.closes.Load() != 0 {
		t.Fatal("reapplying enabled restarted the session")
	}
	if err := f.service.Stop(); err != nil {
		t.Fatal(err)
	}
	if f.service.IsRunning() || f.status.get("connected") != "0" {
		t.Fatal("service did not stop")
	}
	scans := f.scanCount()
	f.notify()
	f.clock.advance(time.Hour)
	if f.scanCount() != scans {
		t.Fatal("disabled service kept discovering")
	}
}

func TestUSBSourceDevicePresentAtStartupAndDescriptorReplay(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	f.start(t)
	session := f.session(t, 0)
	session.send("descriptor/serialnumber", uint32(123456))
	session.send("descriptor/firmware", "1.0.24")
	if f.status.get("connected") == "1" {
		t.Fatal("connected before identifying the project")
	}
	session.send("descriptor/project", "Accessory A")
	usbSourceAwait(t, func() bool { return f.status.get("firmware") == "1.0.24" })
	if f.status.get("serialNumber") != "123456" {
		t.Fatal("descriptors before project were lost")
	}
	if !f.service.IsRunning() || f.clock.activeTimers() != 1 {
		t.Fatal("connected supervisor must have only a health deadline")
	}
}

func TestUSBSourceUnplugRecoveryAndRenumbering(t *testing.T) {
	for _, newName := range []string{"/dev/ttyUSB4", "/dev/ttyUSB7"} {
		t.Run(newName, func(t *testing.T) {
			f := newUSBSourceFixture(t, usbSourceTestDevice)
			f.start(t)
			first := f.connect(t, 0)
			f.setDevices()
			f.notify()
			usbSourceAwait(t, func() bool {
				return first.closes.Load() == 1 && f.status.get("connected") == "0" && f.clock.hasTimer(time.Second)
			})
			scans := f.scanCount()
			f.clock.advance(time.Second)
			usbSourceAwait(t, func() bool { return f.scanCount() > scans && f.clock.hasTimer(2*time.Second) })
			replacement := usbSourceTestDevice
			replacement.Name, replacement.SerialNumber = newName, "device-b"
			f.setDevices(replacement)
			// Polling must recover even without a hotplug notification.
			f.clock.advance(2 * time.Second)
			second := f.connect(t, 1)
			if second.device.Name != newName {
				t.Fatal("port name was cached")
			}
			first.send("descriptor/project", "wrong")
			second.send("descriptor/firmware", "new-session")
			usbSourceAwait(t, func() bool { return f.status.get("firmware") == "new-session" })
			if f.status.get("connected") != "1" {
				t.Fatal("stale session changed current state")
			}
		})
	}
}

func TestUSBSourceReinsertBypassesRecoveryDelay(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	f.start(t)
	first := f.connect(t, 0)
	first.finish(errors.New("USB read failed"))
	usbSourceAwait(t, func() bool { return f.status.get("connected") == "0" && f.clock.hasTimer(time.Second) })
	f.notify()
	f.connect(t, 1) // No clock advance: event bypasses backoff.
}

func TestUSBSourceRecoveryHasNoRetryLimit(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	f.start(t)
	f.connect(t, 0)
	f.setDevices()
	f.notify()
	usbSourceAwait(t, func() bool { return f.clock.hasTimer(time.Second) })
	for i := 0; i < 1005; i++ {
		scans := f.scanCount()
		f.clock.advance(time.Minute)
		usbSourceAwait(t, func() bool { return f.scanCount() > scans && f.clock.activeTimers() > 0 })
	}
	if !f.service.IsRunning() {
		t.Fatal("retry exhaustion disabled supervision")
	}
	f.setDevices(usbSourceTestDevice)
	f.notify()
	f.connect(t, 1)
}

func TestUSBSourceUnchangedReadingsKeepConnectionAlive(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	f.start(t)
	session := f.connect(t, 0)
	for i := 0; i < 3; i++ {
		f.clock.advance(7 * time.Second)
		session.send("measure/flow-mlpermin", float32(42))
		usbSourceAwait(t, func() bool {
			return f.status.get("received") == fmt.Sprint(f.clock.Now().Unix()) && f.clock.hasTimer(8*time.Second)
		})
		if f.status.get("connected") != "1" {
			t.Fatal("unchanged readings caused disconnect")
		}
	}
	f.clock.advance(8 * time.Second)
	usbSourceAwait(t, func() bool { return session.closes.Load() == 1 && f.status.get("connected") == "0" })
	if f.status.get("received") != "0" {
		t.Fatal("lost connection retained freshness")
	}
}

func TestUSBSourceHandshakeRejectsWrongProjectAndTimesOut(t *testing.T) {
	for _, project := range []string{"wrong-device", ""} {
		t.Run(project, func(t *testing.T) {
			f := newUSBSourceFixture(t, usbSourceTestDevice)
			f.start(t)
			session := f.session(t, 0)
			if project != "" {
				session.send("descriptor/project", project)
			} else {
				session.send("measure/flow-mlpermin", float32(42))
				f.clock.advance(8 * time.Second)
			}
			usbSourceAwait(t, func() bool { return session.closes.Load() == 1 && f.clock.hasTimer(time.Second) })
			if f.status.get("connected") != "0" {
				t.Fatal("failed handshake changed enabled state")
			}
		})
	}
}

func TestUSBSourceSelectsLatestDeviceAndHonorsSelectors(t *testing.T) {
	older := usbSourceTestDevice
	older.Product, older.Attached = "BAC.2360.02", time.Unix(100, 0)
	newer := older
	newer.Name, newer.SerialNumber, newer.Path, newer.Attached = "/dev/ttyUSB2", "device-b", "/sys/devices/usb/2", time.Unix(200, 0)
	t.Run("latest attached", func(t *testing.T) {
		f := newUSBSourceFixture(t, newer, older)
		f.start(t)
		if f.connect(t, 0).device != newer {
			t.Fatal("most recently attached device was not selected")
		}
	})
	t.Run("name breaks ties", func(t *testing.T) {
		first, second := older, newer
		first.Attached, second.Attached = time.Time{}, time.Time{}
		f := newUSBSourceFixture(t, first, second)
		f.start(t)
		if f.connect(t, 0).device != first {
			t.Fatal("unordered devices were not chosen by name")
		}
	})
	t.Run("latest after reconnect", func(t *testing.T) {
		f := newUSBSourceFixture(t, older)
		f.start(t)
		f.connect(t, 0).finish(errors.New("read failure"))
		usbSourceAwait(t, func() bool { return f.clock.hasTimer(time.Second) })
		f.setDevices(older, newer)
		f.notify()
		if f.connect(t, 1).device != newer {
			t.Fatal("previous device was preferred over the latest one")
		}
	})
	t.Run("serial selector", func(t *testing.T) {
		f := newUSBSourceFixture(t, older, newer)
		f.service.options.SerialNumber = older.SerialNumber
		f.start(t)
		if f.connect(t, 0).device != older {
			t.Fatal("USB serial selector ignored")
		}
	})
	t.Run("product selector", func(t *testing.T) {
		other := newer
		other.Product = "FT230X Basic UART"
		f := newUSBSourceFixture(t, older, other)
		f.service.options.Product = " bac.2360.02"
		f.start(t)
		if f.connect(t, 0).device != older {
			t.Fatal("USB product selector ignored")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.openCalls != 1 {
			t.Fatal("adapter with another product was opened")
		}
	})
	t.Run("product mismatch is never opened", func(t *testing.T) {
		other := older
		other.Product = ""
		f := newUSBSourceFixture(t, other)
		f.service.options.Product = "BAC.2360.02"
		f.start(t)
		usbSourceAwait(t, func() bool { return f.clock.activeTimers() == 0 })
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.openCalls != 0 {
			t.Fatal("adapter without the product string was opened")
		}
	})
}

func TestUSBSourceWatcherFailureFallsBackAndRestoresEvents(t *testing.T) {
	watches := func(f *usbSourceFixture) int { f.mu.Lock(); defer f.mu.Unlock(); return f.watches }
	f := newUSBSourceFixture(t)
	f.watchError = errors.New("inotify unavailable")
	f.start(t)
	// Without events, a waiting source polls for devices.
	usbSourceAwait(t, func() bool { return f.clock.hasTimer(time.Second) })
	f.setDevices(usbSourceTestDevice)
	f.clock.advance(time.Second)
	first := f.connect(t, 0)
	// A connected session relies on its health deadline, not on polling.
	usbSourceAwait(t, func() bool { return f.clock.activeTimers() == 1 })
	f.clock.advance(7 * time.Second) // Within the health deadline.
	if watches(f) != 2 || first.closes.Load() != 0 {
		t.Fatal("connected source kept polling")
	}
	f.mu.Lock()
	f.watchError = nil
	f.mu.Unlock()
	first.finish(errors.New("USB read failed"))
	usbSourceAwait(t, func() bool { return f.status.get("connected") == "0" && f.clock.hasTimer(time.Second) })
	f.clock.advance(time.Second)
	second := f.connect(t, 1)
	if watches(f) != 3 {
		t.Fatal("recovery did not restore device events")
	}
	scans := f.scanCount()
	f.failures <- errors.New("event queue overflow")
	usbSourceAwait(t, func() bool { return f.scanCount() > scans && f.clock.activeTimers() == 1 })
	if f.status.get("connected") != "1" {
		t.Fatal("event overflow disconnected a present device")
	}
	second.finish(errors.New("USB read failed"))
	usbSourceAwait(t, func() bool { return f.status.get("connected") == "0" && f.clock.hasTimer(time.Second) })
	f.clock.advance(time.Second)
	f.connect(t, 2)
	if watches(f) != 4 {
		t.Fatal("recovery after overflow did not restore device events")
	}
}

func TestUSBSourceOpenFailureRetriesAndCanBeDisabled(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	f.openError = errors.New("serial port busy")
	f.start(t)
	usbSourceAwait(t, func() bool { return f.clock.hasTimer(time.Second) })
	f.mu.Lock()
	f.openError = nil
	f.mu.Unlock()
	f.clock.advance(time.Second)
	f.connect(t, 0)
	if err := f.service.Restart(); err != nil {
		t.Fatal(err)
	}
	f.connect(t, 1)
}

func TestUSBSourceStopCancelsPendingOpen(t *testing.T) {
	f := newUSBSourceFixture(t, usbSourceTestDevice)
	started := make(chan struct{})
	f.openFn = func(ctx context.Context, _ USBDevice) (*usbSourceSession, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.start(t)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("connection attempt did not start")
	}
	done := make(chan struct{})
	go func() { f.service.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel connection attempt")
	}
	if f.service.IsRunning() {
		t.Fatal("service still running after Stop")
	}
}

func TestUSBSourceConcurrentStartStop(t *testing.T) {
	f := newUSBSourceFixture(t)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 10; n++ {
				f.service.Start()
				_ = f.service.IsRunning()
				f.service.Stop()
			}
		}()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent lifecycle calls deadlocked")
	}
}
