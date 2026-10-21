package gotelemetry

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Run receives telemetry until cancellation or a transport error. It never
// discovers devices or reconnects. It owns and closes the current transport on
// return, including on cancellation. Close must unblock Read; without Close,
// reads must have a timeout so cancellation can be observed.
//
// Only one reader may run per Telemetry. Configure Frame before starting Run;
// do not feed Frame concurrently with Run. Publish and subscription methods may
// be called concurrently. Use a new Telemetry instance for a new connection.
func (t *Telemetry) Run(ctx context.Context) error {
	if !t.runMu.TryLock() {
		return errors.New("telemetry reader is already running")
	}
	defer t.runMu.Unlock()
	t.Mutex.Lock()
	transport := t.Transport
	t.Mutex.Unlock()
	if transport == nil || transport.Read == nil {
		return errors.New("transport has no reader")
	}
	var once sync.Once
	closeTransport := func() {
		once.Do(func() {
			if transport.Close != nil {
				_ = transport.Close()
			}
		})
	}
	finished := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		select {
		case <-ctx.Done():
			closeTransport()
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		closeTransport()
		<-closed
	}()

	buffer := make([]byte, IncomingBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := transport.Read(buffer)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, b := range buffer[:n] {
			t.Frame.FeedByte(b)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			// Readers may report timeouts with (0, nil). Avoid a busy loop
			// when a custom reader returns immediately without progress.
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}

// UpdateTelemetry starts listening for incoming messages and processes them.
// New applications should use Run and own their reconnect policy.
func (t *Telemetry) UpdateTelemetry(stopChan chan struct{}) {
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		finished := make(chan struct{})
		defer close(finished)
		go func() {
			select {
			case <-stopChan:
				cancel()
			case <-finished:
			}
		}()
		for {
			_ = t.Run(ctx)
			if ctx.Err() != nil {
				return
			}
			if err := t.reconnect(ctx); err != nil {
				return
			}
		}
	}()
}

// reconnect attempts to re-establish the USB connection.
func (t *Telemetry) reconnect(ctx context.Context) error {
	t.Mutex.Lock()
	previous := t.Transport
	t.Mutex.Unlock()
	if previous == nil || previous.VendorID == "" || previous.ProductID == "" {
		return errors.New("transport has no USB identity")
	}
	log.Println("Attempting to reconnect...")
	transport, err := GetTransportContext(ctx, previous.VendorID, previous.ProductID)
	if err != nil {
		return err
	}
	t.Mutex.Lock()
	t.Transport = transport
	t.Mutex.Unlock()
	log.Printf("Reconnected to USB port: %s", transport.PortName)
	return nil
}
