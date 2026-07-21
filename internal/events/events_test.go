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

func TestRecoverableSubscriberGetsCoalescedResync(t *testing.T) {
	b := NewBus()
	_, resync, unsub := b.SubscribeRecoverable()
	defer unsub()

	for i := 0; i < b.buffer+10; i++ {
		b.Publish(Event{Type: StateChanged, DeviceID: "lamp"})
	}
	select {
	case <-resync:
	case <-time.After(time.Second):
		t.Fatal("missing resync signal after subscriber overflow")
	}
	select {
	case <-resync:
		t.Fatal("overflow burst produced more than one pending resync")
	default:
	}
}

func TestResyncMakesSnapshotAndPublicationOrdered(t *testing.T) {
	b := NewBus()
	sub, unsub := b.Subscribe()
	defer unsub()
	inside := make(chan struct{})
	release := make(chan struct{})
	resyncDone := make(chan struct{})
	go func() {
		b.Resync(func() {
			close(inside)
			<-release
		})
		close(resyncDone)
	}()
	<-inside

	publishDone := make(chan struct{})
	go func() {
		b.Publish(Event{Type: StateChanged, DeviceID: "lamp"})
		close(publishDone)
	}()
	select {
	case <-publishDone:
		t.Fatal("event was published during the protected snapshot")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	<-resyncDone
	<-publishDone
	select {
	case event := <-sub:
		if event.DeviceID != "lamp" {
			t.Fatalf("event device = %q", event.DeviceID)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not delivered after resync")
	}
}
