package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/users"
)

func openTestSocket(t *testing.T, handler http.Handler, token string) (*websocket.Conn, context.Context) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + url.QueryEscape(token)
	conn, response, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial WebSocket: %v (status %d)", err, response.StatusCode)
		}
		t.Fatalf("dial WebSocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, ctx
}

func readUntilType(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) wsMessage {
	t.Helper()
	for {
		var message wsMessage
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			t.Fatalf("read WebSocket frame %q: %v", want, err)
		}
		if message.Type == want {
			return message
		}
	}
}

func TestWebSocketSignalsEveryInventoryMutation(t *testing.T) {
	handler, _, _ := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"))
	conn, ctx := openTestSocket(t, handler, "secret")

	snapshot := readUntilType(t, ctx, conn, "snapshot")
	if snapshot.DeviceID != "desk" || snapshot.State == nil {
		t.Fatalf("initial snapshot = %+v", snapshot)
	}

	if response := deviceRequest(t, handler, http.MethodPatch, "/api/devices/desk", `{"name":"Reading lamp"}`); response.Code != http.StatusOK {
		t.Fatalf("rename status = %d: %s", response.Code, response.Body.String())
	}
	if message := readUntilType(t, ctx, conn, string(events.InventoryChanged)); message.DeviceID != "" || message.State != nil {
		t.Fatalf("rename invalidation leaked device data: %+v", message)
	}

	if response := deviceRequest(t, handler, http.MethodPost, "/api/devices", `{"brand":"test","driver":"lamp","name":"Shelf","mac":"98:77:d5:a2:34:f3"}`); response.Code != http.StatusCreated {
		t.Fatalf("add status = %d: %s", response.Code, response.Body.String())
	}
	readUntilType(t, ctx, conn, string(events.InventoryChanged))

	if response := deviceRequest(t, handler, http.MethodDelete, "/api/devices/desk", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", response.Code, response.Body.String())
	}
	readUntilType(t, ctx, conn, string(events.InventoryChanged))

	if response := deviceRequest(t, handler, http.MethodPut, "/api/devices", `{"version":2,"items":[]}`); response.Code != http.StatusOK {
		t.Fatalf("restore status = %d: %s", response.Code, response.Body.String())
	}
	readUntilType(t, ctx, conn, string(events.InventoryChanged))
}

func TestWebSocketReResolvesGrantsBeforeLaterState(t *testing.T) {
	handler, registry, _, bus := accessServerWithBus(
		t,
		lamp("lamp", "98:77:d5:a2:34:f2"),
		lamp("tv", "98:77:d5:a2:34:f3"),
	)
	user, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatal(err)
	}
	conn, ctx := openTestSocket(t, handler, token)
	if snapshot := readUntilType(t, ctx, conn, "snapshot"); snapshot.DeviceID != "lamp" {
		t.Fatalf("initial snapshot = %+v, want lamp", snapshot)
	}

	body := `{"devices":["lamp","tv"]}`
	if response := as(t, handler, "admin-token", http.MethodPatch, "/api/users/"+user.ID, body); response.Code != http.StatusOK {
		t.Fatalf("grant update status = %d: %s", response.Code, response.Body.String())
	}
	readUntilType(t, ctx, conn, string(events.InventoryChanged))

	bus.Publish(events.Event{
		Type:     events.StateChanged,
		DeviceID: "tv",
		State:    device.State{Online: true, On: true},
	})
	message := readUntilType(t, ctx, conn, string(events.StateChanged))
	if message.DeviceID != "tv" || message.State == nil || !message.State.On {
		t.Fatalf("post-grant state = %+v, want live TV state", message)
	}
}
