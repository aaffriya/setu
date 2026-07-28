package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"setu/internal/device"
	"setu/internal/events"
)

// wsWriteTimeout bounds every event write. The connection context (CloseRead)
// only cancels when a *read* fails, and a phone suspended mid-connection leaves
// a half-open socket whose reads stay silent for a long time: without a write
// deadline a blocked write would keep the goroutine, its bus subscription, and
// the kernel buffers alive until TCP retransmission gives up (~15+ minutes).
// With it, a stuck client is dropped at the next event instead.
const wsWriteTimeout = 10 * time.Second

// wsMessage is what the server pushes to WebSocket clients. "snapshot" is sent
// once on connect for each device; "state_changed" carries live state; and
// "inventory_changed" is a metadata-free hint to re-read the caller's filtered
// device list and session.
type wsMessage struct {
	Type     string        `json:"type"`
	DeviceID string        `json:"device_id,omitempty"`
	State    *device.State `json:"state,omitempty"`
}

// handleWS upgrades to a WebSocket and streams state events to the client. Each
// connection gets its own subscription to the event bus (the bus is the fan-out
// mechanism, so no central client registry is needed). The connection is
// read-only from the server's side: commands go over the JSON API; events come
// back here.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Resolve before the upgrade for the initial snapshot. Inventory-change
	// events re-resolve it below, so grant edits take effect without reconnecting.
	principal := principalOf(r)

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

	sub, resync, unsubscribe := s.bus.SubscribeRecoverable()
	defer unsubscribe()

	// Send an initial snapshot so a freshly-connected client is immediately
	// consistent without waiting for the next change.
	for _, view := range granted(principal, s.mgr.Snapshot()) {
		state := view.State
		msg := wsMessage{Type: "snapshot", DeviceID: view.ID, State: &state}
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
			if ev.Type == events.InventoryChanged {
				// The token may now resolve to a different role/grant set, or no
				// account at all after deletion/rotation. Send the invalidation
				// first so an invalidated browser can discover the 401 over REST,
				// then close a socket whose token is no longer valid.
				next, authenticated := s.resolve(r, true)
				if err := writeMsg(ctx, c, wsMessage{Type: string(ev.Type)}); err != nil {
					return
				}
				if !authenticated {
					return
				}
				principal = next
				continue
			}
			if ev.Type != events.StateChanged {
				continue
			}
			// The bus carries every device. Dropping the ones this account was
			// not granted here is what keeps their state out of the browser —
			// the snapshot above is filtered for the same reason. principal is
			// refreshed by every InventoryChanged event.
			if !principal.CanSee(ev.DeviceID) {
				continue
			}
			state := ev.State
			msg := wsMessage{Type: string(ev.Type), DeviceID: ev.DeviceID, State: &state}
			if err := writeMsg(ctx, c, msg); err != nil {
				return
			}
		case _, ok := <-resync:
			if !ok {
				return
			}
			// This client fell behind and therefore no longer has a complete event
			// history. Close it; the lightweight client reconnects and receives one
			// fresh snapshot instead of displaying a permanently stale state.
			return
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
