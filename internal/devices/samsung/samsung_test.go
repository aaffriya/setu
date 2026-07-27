package samsung

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

type failingTransport struct {
	requests int
}

func (t *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.requests++
	return nil, errors.New("test transport unavailable")
}

func TestCloseReleasesHeldKeyAndPreventsReconnect(t *testing.T) {
	transport := &failingTransport{}
	timerFired := make(chan struct{}, 1)
	b := &base{
		id:      "tv-test",
		mac:     "02:00:00:00:00:10",
		ip:      net.IPv4(127, 0, 0, 1),
		http:    &http.Client{Transport: transport},
		timeout: time.Millisecond,
		heldKey: "KEY_UP",
	}
	b.holdTimer = time.AfterFunc(time.Hour, func() { timerFired <- struct{}{} })

	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	b.holdMu.Lock()
	heldKey, holdTimer := b.heldKey, b.holdTimer
	b.holdMu.Unlock()
	if heldKey != "" || holdTimer != nil {
		t.Fatalf("held key cleanup = key %q, timer %v", heldKey, holdTimer)
	}
	if transport.requests == 0 {
		t.Fatal("close did not attempt the best-effort held-key release")
	}
	requestsAfterClose := transport.requests

	if err := b.sendKeyCmd("Click", "KEY_HOME"); err == nil {
		t.Fatal("closed driver accepted a new key")
	}
	if transport.requests != requestsAfterClose {
		t.Fatal("closed driver attempted to reconnect")
	}
	select {
	case <-timerFired:
		t.Fatal("held-key watchdog fired after close")
	default:
	}
}
