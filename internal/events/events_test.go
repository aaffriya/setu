package events

import (
	"testing"
	"time"

	"setu/internal/device"
)

func TestBusPublishSubscribe(t *testing.T) {
	b := NewBus()
	sub, unsub := b.Subscribe()
	defer unsub()

	b.Publish(Event{Type: StateChanged, DeviceID: "x", State: device.State{On: true}})

	select {
	case ev := <-sub:
		if ev.DeviceID != "x" || !ev.State.On {
			t.Errorf("unexpected event: %+v", ev)
		}
		if ev.Time.IsZero() {
			t.Error("Publish should stamp a non-zero Time")
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestUnsubscribeIsSafe(t *testing.T) {
	b := NewBus()
	_, unsub := b.Subscribe()

	unsub()
	unsub() // calling twice must not panic (sync.Once)

	// Publishing with no live subscribers must not block or panic.
	b.Publish(Event{Type: StateChanged, DeviceID: "y"})
}
