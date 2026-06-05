// Package samsung controls Samsung Tizen Smart TVs (e.g. the AU7700 series).
//
// A TV spans three transports, all implemented here with the standard library
// plus our existing WebSocket dependency:
//
//   - HTTP REST (DIAL) on :8001 — used here for reachability (Poll).
//   - WebSocket over TLS on :8002 — remote keys (power off, volume, navigation).
//     Token-authenticated: the TV returns a token after the user taps "Allow"
//     once; we persist it and reuse it.
//   - Wake-on-LAN (UDP) — power on (unreliable over Wi-Fi; see On).
//
// It implements the Switchable, Volume, KeyControl, and AppControl capabilities.
// This is the blueprint (internal/devices/example) applied to a non-light device,
// showing how new capabilities light up matching UI controls automatically.
package samsung

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/resolver"
)

const (
	Brand      = "Samsung"
	ModelTizen = "tizen"

	restPort       = "8001"
	wsPort         = "8002"
	appName        = "Setu" // shown in the TV's Device Connection Manager
	defaultTimeout = 4 * time.Second
	pairTimeout    = 15 * time.Second       // first connect: time for the user to tap "Allow"
	powerGrace     = 10 * time.Second       // trust an explicit On/Off over REST during the TV's power transition
	wsIdleClose    = 45 * time.Second       // close the reused remote-control socket after this much idle
	volumePace     = 120 * time.Millisecond // gap between volume key presses; below this the TV debounces them (tune if steps drop)

	volumeRailMargin = 4   // extra presses past 0/100 so a full slide truly hits the rail (and re-calibrates the tracked level)
	volumeMaxSteps   = 110 // safety cap on presses per SetVolume
)

// keyPattern restricts remote keys to the documented KEY_* form, so arbitrary
// strings can't be funneled to the TV.
var keyPattern = regexp.MustCompile(`^KEY_[A-Z0-9_]+$`)

// tvApp is a launchable app shortcut: a stable catalog id (what the UI sends
// back in a launch_app command) plus the ordered DIAL ids to try when launching.
// App ids vary by firmware/region, so we attempt each id in turn until one
// launches — a 404 just means "not that id on this TV" (see docs/devices/samsung.md
// §6). Only ids known to map to the SAME app are listed, so a fallback can never
// open the wrong app.
type tvApp struct {
	id     string
	name   string
	launch []string
}

var tvApps = []tvApp{
	{id: "111299001912", name: "YouTube", launch: []string{"111299001912"}},
	{id: "3201907018807", name: "Netflix", launch: []string{"3201907018807", "11101200001", "org.tizen.netflix-app"}},
	{id: "3201512006785", name: "Prime Video", launch: []string{"3201910019365", "org.tizen.ignition", "3201512006785"}},
	{id: "3202410037378", name: "Skypro", launch: []string{"3202410037378"}},
	{id: "3201908019041", name: "Apple Music", launch: []string{"3201908019041"}},
}

// base is the shared Samsung brand foundation: identity, IP resolution, the
// shared HTTP client (REST + WS dial), and the pairing token.
type base struct {
	id, name, series, mac, ipHint string
	arp                           resolver.Resolver
	bus                           *events.Bus
	http                          *http.Client
	timeout                       time.Duration
	tokenPath                     string

	mu      sync.Mutex
	ip      net.IP
	token   string
	state   device.State
	powerAt time.Time // when On/Off was last commanded (start of the grace window)
	powerOn bool      // the power state that command intended

	wsMu   sync.Mutex      // serializes the remote-control socket (dial + writes); guards wsConn/wsIdle
	wsConn *websocket.Conn // reused remote-control socket (nil = none open)
	wsIdle *time.Timer     // idle-closes wsConn after wsIdleClose of no keys
}

func (b *base) ID() string     { return b.id }
func (b *base) Name() string   { return b.name }
func (b *base) Brand() string  { return Brand }
func (b *base) MAC() string    { return b.mac }
func (b *base) Series() string { return b.series }

func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// resolveIP: cached → injected ARP resolver → config hint. (Samsung has no
// simple broadcast discovery like WiZ; SSDP/mDNS would slot in here later.)
func (b *base) resolveIP() (net.IP, error) {
	b.mu.Lock()
	cached := b.ip
	b.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	if b.arp != nil {
		if ip, err := b.arp.Lookup(b.mac); err == nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	if b.ipHint != "" {
		if ip := net.ParseIP(b.ipHint); ip != nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	return nil, fmt.Errorf("samsung %s: cannot resolve ip for mac %s", b.id, b.mac)
}

func (b *base) setIP(ip net.IP) { b.mu.Lock(); b.ip = ip; b.mu.Unlock() }
func (b *base) invalidateIP()   { b.mu.Lock(); b.ip = nil; b.mu.Unlock() }

func (b *base) applyState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	snap := b.state
	b.mu.Unlock()
	if b.bus != nil {
		b.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: b.id, State: snap})
	}
}

func (b *base) updateState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	b.mu.Unlock()
}

// markPower records an explicit On/Off so Poll trusts it over REST reachability
// for a short window. The TV keeps answering REST for a few seconds while it
// powers off (and takes time to answer after a WoL wake), so without this the
// polled power state would briefly flicker against the command just issued.
func (b *base) markPower(on bool) {
	b.mu.Lock()
	b.powerAt = time.Now()
	b.powerOn = on
	b.mu.Unlock()
}

// intendedPower returns the last commanded power state while it's still inside
// the grace window; fresh is false once the window has elapsed (then Poll lets
// live reachability drive the state, so out-of-band power changes are detected).
func (b *base) intendedPower() (on, fresh bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.powerAt.IsZero() || time.Since(b.powerAt) >= powerGrace {
		return false, false
	}
	return b.powerOn, true
}

// --- token persistence -----------------------------------------------------

func (b *base) getToken() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

func (b *base) loadToken() {
	if data, err := os.ReadFile(b.tokenPath); err == nil {
		b.mu.Lock()
		b.token = strings.TrimSpace(string(data))
		b.mu.Unlock()
	}
}

func (b *base) saveToken(tok string) {
	b.mu.Lock()
	changed := tok != b.token
	b.token = tok
	b.mu.Unlock()
	if changed {
		_ = os.WriteFile(b.tokenPath, []byte(tok), 0o600)
	}
}

// --- REST (reachability) ----------------------------------------------------

// reachable reports whether the TV answers its DIAL REST endpoint.
func (b *base) reachable(ctx context.Context) bool {
	ip, err := b.resolveIP()
	if err != nil {
		return false
	}
	u := fmt.Sprintf("http://%s/api/v2/", net.JoinHostPort(ip.String(), restPort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	resp, err := b.http.Do(req)
	if err != nil {
		b.invalidateIP()
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// --- WebSocket remote keys --------------------------------------------------
//
// The remote-control socket is opened once and reused for subsequent keys, then
// idle-closed (wsIdleClose). This avoids a TLS dial + ~500 ms flush on every
// press — the old fresh-socket-per-key was reliable but slow for volume / D-pad
// bursts. A stale socket (the TV drops idle ones) is detected on the next write
// and redialed once, so reliability matches the previous one-shot path.

// sendKey sends one "Click" key, reusing the cached socket or dialing a new one.
func (b *base) sendKey(ctx context.Context, key string) error {
	cmd, err := json.Marshal(map[string]any{
		"method": "ms.remote.control",
		"params": map[string]string{
			"Cmd":          "Click",
			"DataOfCmd":    key,
			"Option":       "false",
			"TypeOfRemote": "SendRemoteKey",
		},
	})
	if err != nil {
		return err
	}

	b.wsMu.Lock()
	defer b.wsMu.Unlock()

	if err := b.writeFrameLocked(ctx, cmd); err != nil {
		// The cached socket was stale/closed — drop it and try once more fresh.
		b.closeWSLocked()
		if err := b.writeFrameLocked(ctx, cmd); err != nil {
			return fmt.Errorf("samsung %s: send key %s: %w", b.id, key, err)
		}
	}
	b.armIdleLocked()
	return nil
}

// writeFrameLocked ensures a live socket and writes one frame on it. wsMu held.
func (b *base) writeFrameLocked(ctx context.Context, frame []byte) error {
	if b.wsConn == nil {
		c, err := b.dialWSLocked(ctx)
		if err != nil {
			return err
		}
		b.wsConn = c
	}
	writeCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.wsConn.Write(writeCtx, websocket.MessageText, frame)
}

// wsControlURL builds the token-authenticated remote-control WebSocket URL.
func (b *base) wsControlURL(ip net.IP) string {
	q := url.Values{}
	q.Set("name", base64.StdEncoding.EncodeToString([]byte(appName)))
	if tok := b.getToken(); tok != "" {
		q.Set("token", tok)
	}
	u := url.URL{
		Scheme:   "wss",
		Host:     net.JoinHostPort(ip.String(), wsPort),
		Path:     "/api/v2/channels/samsung.remote.control",
		RawQuery: q.Encode(),
	}
	return u.String()
}

// dialWSLocked opens a token-authenticated socket, captures/refreshes the token
// from the first (ms.channel.connect) message, and starts a drain reader. wsMu held.
func (b *base) dialWSLocked(ctx context.Context) (*websocket.Conn, error) {
	ip, err := b.resolveIP()
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, b.wsControlURL(ip), &websocket.DialOptions{HTTPClient: b.http})
	if err != nil {
		b.invalidateIP()
		return nil, fmt.Errorf("samsung %s: ws dial: %w", b.id, err)
	}

	// First message is ms.channel.connect with the (new/refreshed) token. On the
	// first-ever pairing it arrives only after the user taps "Allow", so allow
	// pairTimeout for it.
	connCtx, cancelConn := context.WithTimeout(ctx, pairTimeout)
	defer cancelConn()
	if _, data, err := c.Read(connCtx); err == nil {
		b.captureToken(data)
	}
	go b.drainWS(c)
	return c, nil
}

// drainWS keeps reading so the library handles control frames and the read
// buffer never blocks; it refreshes the token if the TV re-emits a connect
// event, and clears the cached socket when the connection ends.
func (b *base) drainWS(c *websocket.Conn) {
	for {
		_, data, err := c.Read(context.Background())
		if err != nil {
			b.wsMu.Lock()
			if b.wsConn == c {
				b.wsConn = nil
				if b.wsIdle != nil {
					b.wsIdle.Stop()
					b.wsIdle = nil
				}
			}
			b.wsMu.Unlock()
			_ = c.Close(websocket.StatusNormalClosure, "")
			return
		}
		b.captureToken(data)
	}
}

// armIdleLocked (re)starts the timer that idle-closes the reused socket. wsMu held.
func (b *base) armIdleLocked() {
	if b.wsIdle != nil {
		b.wsIdle.Stop()
	}
	b.wsIdle = time.AfterFunc(wsIdleClose, func() {
		b.wsMu.Lock()
		b.closeWSLocked()
		b.wsMu.Unlock()
	})
}

// closeWSLocked closes and clears the cached socket (also unblocks drainWS). wsMu held.
func (b *base) closeWSLocked() {
	if b.wsIdle != nil {
		b.wsIdle.Stop()
		b.wsIdle = nil
	}
	if b.wsConn != nil {
		c := b.wsConn
		b.wsConn = nil
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
}

// captureToken persists the pairing token from a server message, if present.
func (b *base) captureToken(data []byte) {
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &ev) == nil && ev.Data.Token != "" {
		b.saveToken(ev.Data.Token)
	}
}

// sendKeyNow runs sendKey with its own timeout context.
func (b *base) sendKeyNow(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout+b.timeout)
	defer cancel()
	return b.sendKey(ctx, key)
}

// installedApps asks the TV for its installed apps over a short-lived socket
// (ms.channel.emit ed.installedApp.get) and returns lowercased name → appId. It
// is the ground truth for app ids, which drift across firmware/region, used to
// self-heal launch ids that 404. Uses its own connection (not the reused remote
// socket) to keep the request/response simple.
func (b *base) installedApps(ctx context.Context) (map[string]string, error) {
	ip, err := b.resolveIP()
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, b.wsControlURL(ip), &websocket.DialOptions{HTTPClient: b.http})
	if err != nil {
		b.invalidateIP()
		return nil, fmt.Errorf("samsung %s: ws dial: %w", b.id, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// connect event carries the token
	connCtx, cancelConn := context.WithTimeout(ctx, pairTimeout)
	if _, data, err := c.Read(connCtx); err == nil {
		b.captureToken(data)
	}
	cancelConn()

	req, err := json.Marshal(map[string]any{
		"method": "ms.channel.emit",
		"params": map[string]string{"event": "ed.installedApp.get", "to": "host"},
	})
	if err != nil {
		return nil, err
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, b.timeout)
	err = c.Write(writeCtx, websocket.MessageText, req)
	cancelWrite()
	if err != nil {
		return nil, err
	}

	// Read until the installedApp.get response arrives (or we run out of time).
	deadline := time.Now().Add(b.timeout + time.Second)
	for time.Now().Before(deadline) {
		readCtx, cancelRead := context.WithTimeout(ctx, b.timeout)
		_, data, err := c.Read(readCtx)
		cancelRead()
		if err != nil {
			return nil, err
		}
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				Data []struct {
					AppID string `json:"appId"`
					Name  string `json:"name"`
				} `json:"data"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &ev) == nil && ev.Event == "ed.installedApp.get" {
			out := make(map[string]string, len(ev.Data.Data))
			for _, a := range ev.Data.Data {
				if a.Name != "" && a.AppID != "" {
					out[strings.ToLower(strings.TrimSpace(a.Name))] = a.AppID
				}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("samsung %s: installed-app list timed out", b.id)
}

// --- Wake-on-LAN ------------------------------------------------------------

// wakeOnLAN broadcasts a magic packet to the TV's MAC.
func (b *base) wakeOnLAN() error {
	norm, err := resolver.NormalizeMAC(b.mac)
	if err != nil {
		return err
	}
	hw, err := hex.DecodeString(norm)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("samsung %s: bad mac %q", b.id, b.mac)
	}
	packet := make([]byte, 0, 6+16*6)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xff)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := enableBroadcast(conn); err != nil {
		return err
	}

	// Spray the magic packet at the limited broadcast and every interface's
	// directed broadcast (e.g. 192.168.0.255), on the two common WoL ports.
	// Directed broadcast is more reliable than 255.255.255.255 for same-subnet
	// WoL. (Note: a Wi-Fi TV in standby may still not honour WoL — see README.)
	sent := 0
	for _, ip := range broadcastIPs() {
		for _, port := range []int{9, 7} {
			if _, err := conn.WriteToUDP(packet, &net.UDPAddr{IP: ip, Port: port}); err == nil {
				sent++
			}
		}
	}
	if sent == 0 {
		return fmt.Errorf("samsung %s: wake-on-lan: no broadcast target reachable", b.id)
	}
	return nil
}

// broadcastIPs returns the limited broadcast plus each non-loopback IPv4
// interface's directed broadcast address (host bits set to 1).
func broadcastIPs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() {
			continue
		}
		mask := ipnet.Mask
		if len(mask) == 16 {
			mask = mask[12:]
		}
		if len(mask) != 4 {
			continue
		}
		out = append(out, net.IP{
			ip4[0] | ^mask[0], ip4[1] | ^mask[1], ip4[2] | ^mask[2], ip4[3] | ^mask[3],
		})
	}
	return out
}

func enableBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}

// ---------------------------------------------------------------------------
// TV: the Tizen TV model — power, volume, and arbitrary remote keys.
// ---------------------------------------------------------------------------

type TV struct {
	base

	discMu     sync.Mutex
	discovered map[string]string // catalog id → launch id learned from the TV (ed.installedApp.get)

	volMu     sync.Mutex
	vol       int                // authoritative tracked volume 0–100 (advanced as keys are sent)
	volCancel context.CancelFunc // cancels an in-flight ramp when a newer SetVolume arrives
}

var (
	_ device.Device       = (*TV)(nil)
	_ device.Switchable   = (*TV)(nil)
	_ device.Volume       = (*TV)(nil)
	_ device.VolumeSetter = (*TV)(nil)
	_ device.KeyControl   = (*TV)(nil)
	_ device.AppControl   = (*TV)(nil)
	_ device.Pollable     = (*TV)(nil)
)

func (t *TV) Model() string { return ModelTizen }

func (t *TV) Capabilities() []string {
	return []string{device.CapSwitch, device.CapVolume, device.CapKey, device.CapApp}
}

// On powers the TV on via Wake-on-LAN. NOTE: WoL is unreliable over Wi-Fi (this
// model is wireless); if it doesn't wake, the next poll corrects the On state
// (Online stays true — see Poll — so the power control remains usable to retry).
func (t *TV) On() error {
	if err := t.wakeOnLAN(); err != nil {
		return err
	}
	t.markPower(true)
	t.applyState(func(s *device.State) { s.Online = true; s.On = true })
	return nil
}

// Off powers the TV off via the remote KEY_POWER (reliable when the TV is on).
// The TV stays Online (off ≠ offline): an off TV no longer answers REST but can
// still be woken by Wake-on-LAN, so we keep it controllable.
func (t *TV) Off() error {
	if err := t.sendKeyNow("KEY_POWER"); err != nil {
		return err
	}
	t.markPower(false)
	t.applyState(func(s *device.State) { s.Online = true; s.On = false })
	return nil
}

func (t *TV) VolumeUp() error   { return t.bumpVolume("KEY_VOLUP", +1) }
func (t *TV) VolumeDown() error { return t.bumpVolume("KEY_VOLDOWN", -1) }
func (t *TV) ToggleMute() error { return t.sendKeyNow("KEY_MUTE") }

// bumpVolume sends one volume key and advances the tracked level by one.
func (t *TV) bumpVolume(key string, d int) error {
	if err := t.sendKeyNow(key); err != nil {
		return err
	}
	t.volMu.Lock()
	t.vol = clampVol(t.vol + d)
	v := t.vol
	t.volMu.Unlock()
	t.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Volume = v })
	return nil
}

// SetVolume drives the TV to an absolute level (0–100). The remote channel has no
// "set volume" and debounces rapid identical keys, so it steps with paced up/down
// presses. The ramp runs in the background (the UI reflects the target at once)
// and a newer SetVolume supersedes an in-flight one. Sliding fully to 0 or 100
// overshoots to the rail, re-calibrating the tracked level if it had drifted
// (e.g. from the physical remote).
func (t *TV) SetVolume(pct int) error {
	pct = clampVol(pct)

	t.volMu.Lock()
	if t.volCancel != nil {
		t.volCancel() // supersede an in-flight ramp
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.volCancel = cancel
	cur := t.vol
	t.volMu.Unlock()

	key, dir, steps := "KEY_VOLUP", +1, pct-cur
	if steps < 0 {
		key, dir, steps = "KEY_VOLDOWN", -1, -steps
	}
	switch pct {
	case 0:
		key, dir, steps = "KEY_VOLDOWN", -1, cur+volumeRailMargin
	case 100:
		key, dir, steps = "KEY_VOLUP", +1, (100-cur)+volumeRailMargin
	}
	if steps > volumeMaxSteps {
		steps = volumeMaxSteps
	}

	// Reflect the target immediately; the ramp catches up in the background.
	t.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Volume = pct })
	go t.rampVolume(ctx, key, dir, steps, pct)
	return nil
}

// rampVolume presses the volume key `steps` times, pacing each, until done or
// superseded; it advances the tracked level per press and snaps it to the final
// target on completion.
func (t *TV) rampVolume(ctx context.Context, key string, dir, steps, target int) {
	for i := 0; i < steps; i++ {
		if ctx.Err() != nil {
			return
		}
		if err := t.sendKeyNow(key); err != nil {
			return
		}
		t.volMu.Lock()
		t.vol = clampVol(t.vol + dir)
		t.volMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(volumePace):
		}
	}
	// Completed without being superseded: the tracked level is now exact.
	t.volMu.Lock()
	t.vol = target
	t.volMu.Unlock()
}

func clampVol(v int) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

// SendKey sends an arbitrary validated remote key (e.g. KEY_HOME, KEY_UP).
func (t *TV) SendKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("samsung %s: invalid key %q", t.id, key)
	}
	if key == "KEY_FACTORY" {
		return fmt.Errorf("samsung %s: refusing service-menu key", t.id)
	}
	return t.sendKeyNow(key)
}

// Apps lists the launchable streaming apps exposed as UI shortcuts.
func (t *TV) Apps() []device.App {
	out := make([]device.App, len(tvApps))
	for i, a := range tvApps {
		out[i] = device.App{ID: a.id, Name: a.name}
	}
	return out
}

// LaunchApp opens an app over REST (DIAL: POST /api/v2/applications/<id>; see
// docs/devices/samsung.md §2). id must be one of Apps(); the app's DIAL ids are
// tried in order until one launches (a 404 means that id isn't installed under
// that name). The first launch of an app shows a one-time "Allow" prompt on the TV.
func (t *TV) LaunchApp(id string) error {
	var app *tvApp
	for i := range tvApps {
		if tvApps[i].id == id {
			app = &tvApps[i]
			break
		}
	}
	if app == nil {
		return fmt.Errorf("samsung %s: unknown app %q", t.id, id)
	}

	ip, err := t.resolveIP()
	if err != nil {
		return err
	}

	// Try a previously self-healed id first, then the static candidate list.
	candidates := app.launch
	if learned := t.learnedID(app.id); learned != "" {
		candidates = append([]string{learned}, candidates...)
	}

	var lastErr error
	for _, cid := range candidates {
		status, err := t.launchOne(ip, cid)
		if err != nil {
			t.invalidateIP()
			return fmt.Errorf("samsung %s: launch app %s: %w", t.id, app.name, err)
		}
		if status == http.StatusOK || status == http.StatusCreated {
			t.applyState(func(s *device.State) { s.Online = true; s.On = true })
			return nil
		}
		lastErr = fmt.Errorf("samsung %s: launch app %s (id %s): status %d", t.id, app.name, cid, status)
	}

	// Every known id 404'd — ask the TV for its real app list, match by name, and
	// launch that id (caching it so next time we skip straight to it).
	ctx, cancel := context.WithTimeout(context.Background(), 2*t.timeout+pairTimeout)
	defer cancel()
	if apps, derr := t.installedApps(ctx); derr == nil {
		if rid := matchInstalled(apps, app.name); rid != "" {
			status, err := t.launchOne(ip, rid)
			if err == nil && (status == http.StatusOK || status == http.StatusCreated) {
				t.rememberID(app.id, rid)
				t.applyState(func(s *device.State) { s.Online = true; s.On = true })
				return nil
			}
		}
	}
	return lastErr
}

// learnedID returns a launch id previously discovered for a catalog app.
func (t *TV) learnedID(catalogID string) string {
	t.discMu.Lock()
	defer t.discMu.Unlock()
	return t.discovered[catalogID]
}

// rememberID caches a discovered launch id for a catalog app.
func (t *TV) rememberID(catalogID, launchID string) {
	t.discMu.Lock()
	if t.discovered == nil {
		t.discovered = make(map[string]string)
	}
	t.discovered[catalogID] = launchID
	t.discMu.Unlock()
}

// matchInstalled finds an installed app id for the given display name: exact
// (case-insensitive) first, then a contains-match either way (e.g. catalog
// "Prime Video" vs. installed "Amazon Prime Video").
func matchInstalled(apps map[string]string, name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if id, ok := apps[n]; ok {
		return id
	}
	for inst, id := range apps {
		if strings.Contains(inst, n) || strings.Contains(n, inst) {
			return id
		}
	}
	return ""
}

// launchOne POSTs a single DIAL launch and returns the HTTP status (a transport
// error is returned separately so the caller can stop trying other ids).
func (t *TV) launchOne(ip net.IP, appID string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	u := fmt.Sprintf("http://%s/api/v2/applications/%s", net.JoinHostPort(ip.String(), restPort), url.PathEscape(appID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// Poll reflects the TV's real power state, like the WiZ bulb's getPilot poll: it
// reads REST reachability as the live power signal, so turning the TV on/off
// out-of-band (e.g. with the physical remote) shows up in the UI on the next
// tick. REST reachability is a *power* proxy, not a presence proxy — an off TV
// stops answering REST but can still be woken by Wake-on-LAN — so the TV is
// reported Online whenever its address resolves (config hint / ARP; WoL works by
// MAC regardless). That keeps off ≠ offline, so the power control stays usable to
// wake it. Right after an explicit On/Off we trust the command for a short grace
// window (see markPower): the TV keeps answering REST for a few seconds while it
// powers down, which would otherwise flicker the state. (We can't read volume.)
func (t *TV) Poll() (device.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	_, err := t.resolveIP()
	known := err == nil
	reachable := known && t.reachable(ctx)
	intended, fresh := t.intendedPower()
	t.updateState(func(s *device.State) {
		s.Online = known
		if fresh {
			s.On = intended // just commanded — hold it through the power transition
		} else {
			s.On = reachable // live signal: reachable ⇒ on, unreachable ⇒ off
		}
	})
	return t.State(), nil
}

// New builds a Samsung TV from its config entry (matches config.Constructor).
func New(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	// The TV serves its WebSocket/HTTPS with a self-signed cert, so we must skip
	// verification. The target is a specific LAN device resolved from its MAC.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed TV cert
	}
	// No Client.Timeout: it would abort the long-lived WebSocket; we use context
	// deadlines per operation instead.
	client := &http.Client{Transport: transport}

	dir := os.Getenv("SETU_STATE_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	t := &TV{base: base{
		id:        spec.ID,
		name:      spec.Name,
		series:    spec.Series,
		mac:       spec.MAC,
		ipHint:    spec.IP,
		arp:       deps.Resolver,
		bus:       deps.Bus,
		http:      client,
		timeout:   defaultTimeout,
		tokenPath: filepath.Join(dir, "setu-samsung-"+sanitizeID(spec.ID)+".token"),
	}}
	t.loadToken()
	return t, nil
}

// Register wires Samsung models into the factory (called from cmd/setu/main.go).
func Register(f *config.Factory) {
	f.Register(Brand, ModelTizen, New)
}

// sanitizeID makes a config id safe to embed in a token filename.
func sanitizeID(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	if sb.Len() == 0 {
		return "device"
	}
	return sb.String()
}
