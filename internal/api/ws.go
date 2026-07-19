package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"setu/internal/device"
)

// wsWriteTimeout bounds every event write. The connection context (CloseRead)
// only cancels when a *read* fails, and a phone suspended mid-connection leaves
// a half-open socket whose reads stay silent for a long time: without a write
// deadline a blocked write would keep the goroutine, its bus subscription, and
// the kernel buffers alive until TCP retransmission gives up (~15+ minutes).
// With it, a stuck client is dropped at the next event instead.
const wsWriteTimeout = 10 * time.Second

// wsMessage is what the server pushes to WebSocket clients. "snapshot" is sent
// once on connect for each device; "state_changed" is sent on every change.
type wsMessage struct {
	Type     string       `json:"type"`
	DeviceID string       `json:"device_id"`
	State    device.State `json:"state"`
}

// handleWS upgrades to a WebSocket and streams state events to the client. Each
// connection gets its own subscription to the event bus (the bus is the fan-out
// mechanism, so no central client registry is needed). The connection is
// read-only from the server's side: commands go over the JSON API; events come
// back here.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The app is same-origin and token-protected; accept any Origin so
		// access via LAN IP, hostname, or tunnel all work.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		s.log.Debug("ws accept failed", "err", err)
		return
	}
	defer c.CloseNow()
	if s.poller != nil {
		s.poller.Activity()
	}

	// CloseRead discards any client→server frames and returns a context that is
	// cancelled when the client disconnects.
	ctx := c.CloseRead(r.Context())

	sub, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	// Send an initial snapshot so a freshly-connected client is immediately
	// consistent without waiting for the next change.
	for _, view := range s.mgr.Snapshot() {
		msg := wsMessage{Type: "snapshot", DeviceID: view.ID, State: view.State}
		if err := writeMsg(ctx, c, msg); err != nil {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			msg := wsMessage{Type: string(ev.Type), DeviceID: ev.DeviceID, State: ev.State}
			if err := writeMsg(ctx, c, msg); err != nil {
				return
			}
		}
	}
}

// writeMsg writes one message with wsWriteTimeout applied (see the constant for
// why the connection context alone is not enough).
func writeMsg(ctx context.Context, c *websocket.Conn, msg wsMessage) error {
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return wsjson.Write(wctx, c, msg)
}
