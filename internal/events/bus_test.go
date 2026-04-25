package events

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestBus_basicPublish(t *testing.T) {
	bus := NewBus(0)
	var count int32
	bus.Subscribe(func(ev Event) {
		atomic.AddInt32(&count, 1)
	})

	bus.Publish(Event{Type: EvDeviceJoin, At: time.Now()})
	bus.Publish(Event{Type: EvDeviceLeave, At: time.Now()})

	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("handler called %d times, want 2", count)
	}
}

func TestBus_multipleSubscribers(t *testing.T) {
	bus := NewBus(0)
	var a, b int32
	bus.Subscribe(func(ev Event) { atomic.AddInt32(&a, 1) })
	bus.Subscribe(func(ev Event) { atomic.AddInt32(&b, 1) })

	bus.Publish(Event{Type: EvWANIPChange, At: time.Now()})

	if atomic.LoadInt32(&a) != 1 || atomic.LoadInt32(&b) != 1 {
		t.Errorf("a=%d b=%d, both should be 1", a, b)
	}
}

func TestBus_cooldownSuppressesRepeat(t *testing.T) {
	bus := NewBus(60) // 60s cooldown for alerts
	var count int32
	bus.Subscribe(func(ev Event) {
		atomic.AddInt32(&count, 1)
	})

	// Publish high CPU twice within cooldown
	bus.Publish(Event{Type: EvHighCPU, At: time.Now()})
	bus.Publish(Event{Type: EvHighCPU, At: time.Now()})
	bus.Publish(Event{Type: EvHighCPU, At: time.Now()})

	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("high CPU should only fire once in cooldown, got %d", count)
	}
}

func TestBus_cooldownDoesNotAffectOtherEvents(t *testing.T) {
	bus := NewBus(60)
	var count int32
	bus.Subscribe(func(ev Event) {
		atomic.AddInt32(&count, 1)
	})

	bus.Publish(Event{Type: EvHighCPU, At: time.Now()})
	// Different event type — no cooldown shared
	bus.Publish(Event{Type: EvHighMemory, At: time.Now()})
	bus.Publish(Event{Type: EvDeviceJoin, At: time.Now()})

	if atomic.LoadInt32(&count) != 3 {
		t.Errorf("different event types should not share cooldown, got %d", count)
	}
}

func TestBus_deviceEventsNoCooldown(t *testing.T) {
	bus := NewBus(300)
	var count int32
	bus.Subscribe(func(ev Event) { atomic.AddInt32(&count, 1) })

	// Device events have no default cooldown — all fire
	bus.Publish(Event{Type: EvDeviceJoin, At: time.Now()})
	bus.Publish(Event{Type: EvDeviceJoin, At: time.Now()})

	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("device join has no cooldown, got %d", count)
	}
}

func TestBus_ifaceDownCooldown(t *testing.T) {
	bus := NewBus(0) // use default cooldowns for iface events
	var count int32
	bus.Subscribe(func(ev Event) { atomic.AddInt32(&count, 1) })

	bus.Publish(Event{Type: EvIfaceDown, At: time.Now()})
	bus.Publish(Event{Type: EvIfaceDown, At: time.Now()})

	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("iface down has default cooldown, got %d", count)
	}
}

func TestBus_noSubscriberOk(t *testing.T) {
	bus := NewBus(0)
	// should not panic
	bus.Publish(Event{Type: EvRebootDetected, At: time.Now()})
}

func TestBus_eventPayloadDelivered(t *testing.T) {
	bus := NewBus(0)
	var received Event
	bus.Subscribe(func(ev Event) { received = ev })

	ev := Event{
		Type:    events_DeviceJoin(),
		Payload: DevicePayload{MAC: "aa:bb:cc", IP: "192.168.1.1", Hostname: "test"},
		At:      time.Unix(1700000000, 0),
	}
	bus.Publish(ev)

	p, ok := received.Payload.(DevicePayload)
	if !ok {
		t.Fatal("payload type assertion failed")
	}
	if p.Hostname != "test" {
		t.Errorf("Hostname: got %q want test", p.Hostname)
	}
}

// helper to avoid import cycle (type alias in same package)
func events_DeviceJoin() EventType { return EvDeviceJoin }
