// Package samsung controls Samsung Tizen Smart TVs (e.g. the AU7700 series).
//
// A TV spans four transports, all implemented here with the standard library
// plus our existing WebSocket dependency:
//
//   - HTTP REST (DIAL) on :8001 — reachability (Poll) and app launch.
//   - WebSocket over TLS on :8002 — remote keys (click and press/hold), text
//     input, and TV-side events (IME focus/typing). Token-authenticated: the TV
//     returns a token after the user taps "Allow" once; we persist and reuse it.
//     The socket is kept open while the TV is on so its events stream in.
//   - UPnP SOAP on :9197 (MediaRenderer RenderingControl) — absolute volume %
//     and mute, both settable and readable back.
//   - Wake-on-LAN (UDP) — power on (unreliable over Wi-Fi; see On).
//
// It implements the Switchable, Volume(+Setter), KeyControl(+Hold), TextInput,
// and AppControl capabilities. This is the blueprint (internal/devices/example)
// applied to a non-light device, showing how new capabilities light up matching
// UI controls automatically.
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
	"strconv"
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
	upnpPort       = "9197" // MediaRenderer (RenderingControl: absolute volume + mute)
	appName        = "Setu" // shown in the TV's Device Connection Manager
	defaultTimeout = 4 * time.Second
	pairTimeout    = 15 * time.Second // first connect: time for the user to tap "Allow"
	powerGrace     = 10 * time.Second // trust an explicit On/Off over REST during the TV's power transition
	holdMax        = 10 * time.Second // watchdog: auto-release a held key the client never released

	// maxTextLen mirrors the TV IME's entrylimit (seen in ms.remote.imeStart).
	maxTextLen = 255
)

// RenderingControl SOAP endpoint (see docs/devices/samsung.md; the control URL
// is confirmed against this unit's MediaRenderer description at :9197/dmr).
const (
	rcControlPath = "/upnp/control/RenderingControl1"
	rcService     = "urn:schemas-upnp-org:service:RenderingControl:1"
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

	wsMu   sync.Mutex      // serializes the remote-control socket (dial + writes); guards wsConn
	wsConn *websocket.Conn // reused remote-control socket (nil = none open)

	holdMu    sync.Mutex  // guards the held-key bookkeeping below
	heldKey   string      // key currently held down via Press ("" = none)
	holdTimer *time.Timer // watchdog that auto-releases heldKey after holdMax
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

// --- WebSocket remote keys & events ------------------------------------------
//
// The remote-control socket is opened once and kept open while the TV is on
// (Poll redials it if it drops — see ensureEvents). Besides avoiding a TLS dial
// + ~500 ms flush per key, the open socket is how TV-side events reach us: IME
// focus/typing (ms.remote.ime*) and token refreshes stream in through drainWS.
// A stale socket is detected on the next write and redialed once, so key sends
// stay as reliable as the old one-shot path.

// keyParams builds the ms.remote.control params for one key frame.
// cmd is "Click" (tap), "Press" (hold down), or "Release" (let up).
func keyParams(cmd, key string) map[string]string {
	return map[string]string{
		"Cmd":          cmd,
		"DataOfCmd":    key,
		"Option":       "false",
		"TypeOfRemote": "SendRemoteKey",
	}
}

// writeRemote marshals and writes ms.remote.control frames on the reused
// socket, redialing once if the cached socket went stale. Multi-frame payloads
// (text input) are retried from the start — resending the full text buffer is
// idempotent on the TV.
func (b *base) writeRemote(ctx context.Context, params ...map[string]string) error {
	frames := make([][]byte, len(params))
	for i, p := range params {
		f, err := json.Marshal(map[string]any{"method": "ms.remote.control", "params": p})
		if err != nil {
			return err
		}
		frames[i] = f
	}

	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	if err := b.writeFramesLocked(ctx, frames); err != nil {
		b.closeWSLocked()
		return b.writeFramesLocked(ctx, frames)
	}
	return nil
}

func (b *base) writeFramesLocked(ctx context.Context, frames [][]byte) error {
	for _, f := range frames {
		if err := b.writeFrameLocked(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// sendKeyCmd sends one key frame with its own timeout context.
func (b *base) sendKeyCmd(cmd, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout+b.timeout)
	defer cancel()
	if err := b.writeRemote(ctx, keyParams(cmd, key)); err != nil {
		return fmt.Errorf("samsung %s: send key %s %s: %w", b.id, cmd, key, err)
	}
	return nil
}

// clickKey taps a key (press and release in one). Any held key is released
// first: while a key is stuck down the TV ignores every other key.
func (b *base) clickKey(key string) error {
	b.releaseHeld()
	return b.sendKeyCmd("Click", key)
}

// pressKey holds a key down. The matching Release is guaranteed without
// trusting the caller: an explicit releaseKey, a newer press/click superseding
// it, or the holdMax watchdog — whichever comes first.
func (b *base) pressKey(key string) error {
	b.releaseHeld()
	if err := b.sendKeyCmd("Press", key); err != nil {
		return err
	}
	b.holdMu.Lock()
	b.heldKey = key
	b.holdTimer = time.AfterFunc(holdMax, b.releaseHeld)
	b.holdMu.Unlock()
	return nil
}

// releaseKey lets a key up. The Release frame is always sent even if our
// bookkeeping shows nothing held — an extra Release is harmless, a missed one
// freezes the TV's remote channel.
func (b *base) releaseKey(key string) error {
	b.holdMu.Lock()
	if b.heldKey == key {
		b.stopHoldLocked()
	}
	b.holdMu.Unlock()
	return b.sendKeyCmd("Release", key)
}

// releaseHeld releases whatever key is currently held, if any. Best-effort by
// design: it redials if the socket dropped, since the TV's stuck-key state
// survives reconnects and only a Release clears it.
func (b *base) releaseHeld() {
	b.holdMu.Lock()
	key := b.heldKey
	b.stopHoldLocked()
	b.holdMu.Unlock()
	if key != "" {
		_ = b.sendKeyCmd("Release", key)
	}
}

func (b *base) stopHoldLocked() {
	b.heldKey = ""
	if b.holdTimer != nil {
		b.holdTimer.Stop()
		b.holdTimer = nil
	}
}

// sendText types into a text field focused on the TV: the base64 payload frame
// (SendInputString) then the commit frame (SendInputEnd). The TV echoes the
// resulting buffer back as ms.remote.imeUpdate (handled in handleEvent).
func (b *base) sendText(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout+b.timeout)
	defer cancel()
	err := b.writeRemote(ctx,
		map[string]string{
			"Cmd":          base64.StdEncoding.EncodeToString([]byte(text)),
			"DataOfCmd":    "base64",
			"TypeOfRemote": "SendInputString",
		},
		map[string]string{"TypeOfRemote": "SendInputEnd"},
	)
	if err != nil {
		return fmt.Errorf("samsung %s: send text: %w", b.id, err)
	}
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
		b.handleEvent(data)
	}
	go b.drainWS(c)
	return c, nil
}

// drainWS keeps reading so the library handles control frames (the TV PINGs
// every ~minute and drops the socket without a PONG) and TV-side events reach
// handleEvent. It clears the cached socket — and any stale IME state — when the
// connection ends; Poll redials while the TV is on.
func (b *base) drainWS(c *websocket.Conn) {
	for {
		_, data, err := c.Read(context.Background())
		if err != nil {
			b.wsMu.Lock()
			if b.wsConn == c {
				b.wsConn = nil
			}
			b.wsMu.Unlock()
			_ = c.Close(websocket.StatusNormalClosure, "")
			b.clearTextInput()
			return
		}
		b.handleEvent(data)
	}
}

// ensureEvents keeps the event socket connected (called from Poll while the TV
// is reachable), so IME events arrive without a command having to open the
// socket first. It never dials without a token: an unpaired dial pops the
// "Allow" prompt on the TV, which a background poller must not do.
func (b *base) ensureEvents(ctx context.Context) {
	if b.getToken() == "" {
		return
	}
	b.wsMu.Lock()
	defer b.wsMu.Unlock()
	if b.wsConn != nil {
		return
	}
	if c, err := b.dialWSLocked(ctx); err == nil {
		b.wsConn = c
	}
}

// closeWSLocked closes and clears the cached socket (also unblocks drainWS). wsMu held.
func (b *base) closeWSLocked() {
	if b.wsConn != nil {
		c := b.wsConn
		b.wsConn = nil
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
}

// handleEvent reacts to one server message: token capture/refresh on
// ms.channel.connect, and the TV-side text-input lifecycle —
//
//	ms.remote.imeStart  → an input field gained focus (TV keyboard open)
//	ms.remote.imeUpdate → the field's full current contents, base64
//	ms.remote.imeEnd    → input committed/closed
//
// imeEnd is not guaranteed (backing out of a field emits nothing), so
// clearTextInput is also called from the focus-leaving paths (SendKey of
// KEY_RETURN/KEY_HOME/…, LaunchApp, Off, socket loss).
func (b *base) handleEvent(data []byte) {
	var ev struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Event {
	case "ms.channel.connect":
		var d struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(ev.Data, &d) == nil && d.Token != "" {
			b.saveToken(d.Token)
		}
	case "ms.remote.imeStart":
		b.applyState(func(s *device.State) { s.TextActive = true; s.TextValue = "" })
	case "ms.remote.imeUpdate":
		var enc string
		if json.Unmarshal(ev.Data, &enc) != nil {
			return
		}
		txt, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return
		}
		b.applyState(func(s *device.State) { s.TextActive = true; s.TextValue = string(txt) })
	case "ms.remote.imeEnd":
		b.clearTextInput()
	}
}

// clearTextInput drops the mirrored TV text-field state, if any.
func (b *base) clearTextInput() {
	b.mu.Lock()
	stale := b.state.TextActive || b.state.TextValue != ""
	b.mu.Unlock()
	if stale {
		b.applyState(func(s *device.State) { s.TextActive = false; s.TextValue = "" })
	}
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
		b.handleEvent(data)
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

// --- UPnP volume & mute (RenderingControl SOAP) ------------------------------
//
// The remote key channel has no absolute volume and KEY_MUTE is a blind toggle.
// The TV's MediaRenderer RenderingControl service gives both, settable AND
// readable back (verified live on the AU7700), so the volume slider and mute
// are built on it: one SOAP call each, real state on every Poll.

var (
	upnpVolRe  = regexp.MustCompile(`<CurrentVolume>([0-9]+)</CurrentVolume>`)
	upnpMuteRe = regexp.MustCompile(`<CurrentMute>([01])</CurrentMute>`)
)

// soapRC performs one RenderingControl action. args is the action-specific tail
// after the fixed InstanceID/Channel pair; the response body is returned for
// the Get* actions to parse.
func (b *base) soapRC(ctx context.Context, action, args string) (string, error) {
	ip, err := b.resolveIP()
	if err != nil {
		return "", err
	}
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="` + rcService + `">` +
		`<InstanceID>0</InstanceID><Channel>Master</Channel>` + args +
		`</u:` + action + `></s:Body></s:Envelope>`

	reqCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	u := fmt.Sprintf("http://%s%s", net.JoinHostPort(ip.String(), upnpPort), rcControlPath)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"`+rcService+`#`+action+`"`)

	resp, err := b.http.Do(req)
	if err != nil {
		b.invalidateIP()
		return "", fmt.Errorf("samsung %s: upnp %s: %w", b.id, action, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("samsung %s: upnp %s: status %d", b.id, action, resp.StatusCode)
	}
	return string(data), nil
}

func (b *base) upnpSetVolume(ctx context.Context, pct int) error {
	_, err := b.soapRC(ctx, "SetVolume", fmt.Sprintf("<DesiredVolume>%d</DesiredVolume>", pct))
	return err
}

func (b *base) upnpVolume(ctx context.Context) (int, error) {
	body, err := b.soapRC(ctx, "GetVolume", "")
	if err != nil {
		return 0, err
	}
	m := upnpVolRe.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("samsung %s: upnp GetVolume: no CurrentVolume in response", b.id)
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	return clampVol(v), nil
}

func (b *base) upnpSetMute(ctx context.Context, mute bool) error {
	val := "0"
	if mute {
		val = "1"
	}
	_, err := b.soapRC(ctx, "SetMute", "<DesiredMute>"+val+"</DesiredMute>")
	return err
}

func (b *base) upnpMute(ctx context.Context) (bool, error) {
	body, err := b.soapRC(ctx, "GetMute", "")
	if err != nil {
		return false, err
	}
	m := upnpMuteRe.FindStringSubmatch(body)
	if m == nil {
		return false, fmt.Errorf("samsung %s: upnp GetMute: no CurrentMute in response", b.id)
	}
	return m[1] == "1", nil
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
}

var (
	_ device.Device       = (*TV)(nil)
	_ device.Switchable   = (*TV)(nil)
	_ device.Volume       = (*TV)(nil)
	_ device.VolumeSetter = (*TV)(nil)
	_ device.KeyControl   = (*TV)(nil)
	_ device.KeyHold      = (*TV)(nil)
	_ device.TextInput    = (*TV)(nil)
	_ device.AppControl   = (*TV)(nil)
	_ device.Pollable     = (*TV)(nil)
)

func (t *TV) Model() string { return ModelTizen }

func (t *TV) Capabilities() []string {
	return []string{
		device.CapSwitch, device.CapVolume,
		device.CapKey, device.CapKeyHold,
		device.CapApp, device.CapText,
	}
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
	if err := t.clickKey("KEY_POWER"); err != nil {
		return err
	}
	t.markPower(false)
	t.clearTextInput()
	t.applyState(func(s *device.State) { s.Online = true; s.On = false })
	return nil
}

func (t *TV) VolumeUp() error   { return t.stepVolume("KEY_VOLUP", +1) }
func (t *TV) VolumeDown() error { return t.stepVolume("KEY_VOLDOWN", -1) }

// stepVolume nudges the volume with a remote key (which shows the TV's own
// volume OSD), then reads the real level back over UPnP. The brief pause lets
// the TV apply the key first — an immediate read returns the pre-step level
// (seen live). If the read fails, the last known level is advanced by one and
// the next Poll corrects it.
func (t *TV) stepVolume(key string, d int) error {
	if err := t.clickKey(key); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	v, err := t.upnpVolume(ctx)
	if err != nil {
		v = clampVol(t.State().Volume + d)
	}
	t.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Volume = v })
	return nil
}

// SetVolume sets the absolute level (0–100) in one UPnP SetVolume call — no key
// stepping, no tracked estimate; the slider is the TV's real volume.
func (t *TV) SetVolume(pct int) error {
	pct = clampVol(pct)
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	if err := t.upnpSetVolume(ctx, pct); err != nil {
		return err
	}
	t.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Volume = pct })
	return nil
}

// ToggleMute reads the real mute state and sets the opposite over UPnP.
// (KEY_MUTE is a blind toggle — it can't tell the app whether the TV is now
// muted; GetMute → SetMute is deterministic and keeps State.Muted truthful.)
func (t *TV) ToggleMute() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*t.timeout)
	defer cancel()
	cur, err := t.upnpMute(ctx)
	if err != nil {
		return err
	}
	if err := t.upnpSetMute(ctx, !cur); err != nil {
		return err
	}
	t.applyState(func(s *device.State) { s.Online = true; s.On = true; s.Muted = !cur })
	return nil
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

// checkKey validates a remote key name (and refuses the service menu).
func (t *TV) checkKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("samsung %s: invalid key %q", t.id, key)
	}
	if key == "KEY_FACTORY" {
		return fmt.Errorf("samsung %s: refusing service-menu key", t.id)
	}
	return nil
}

// focusLeavers are keys that take focus away from a TV text field without the
// TV emitting ms.remote.imeEnd, so the mirrored text state is cleared locally.
var focusLeavers = map[string]bool{
	"KEY_RETURN": true, "KEY_HOME": true, "KEY_EXIT": true, "KEY_POWER": true,
}

// SendKey taps an arbitrary validated remote key (e.g. KEY_HOME, KEY_UP).
func (t *TV) SendKey(key string) error {
	if err := t.checkKey(key); err != nil {
		return err
	}
	if err := t.clickKey(key); err != nil {
		return err
	}
	if focusLeavers[key] {
		t.clearTextInput()
	}
	return nil
}

// PressKey holds a remote key down (fast scroll etc.). The release is
// guaranteed by the base: an explicit ReleaseKey, the next key superseding it,
// or the holdMax watchdog — a stuck Press would freeze the TV's remote channel.
func (t *TV) PressKey(key string) error {
	if err := t.checkKey(key); err != nil {
		return err
	}
	return t.pressKey(key)
}

// ReleaseKey lets a held remote key up.
func (t *TV) ReleaseKey(key string) error {
	if err := t.checkKey(key); err != nil {
		return err
	}
	return t.releaseKey(key)
}

// SendText types into the text field currently focused on the TV (the TV
// ignores it when no field is focused; State.TextActive reports focus live).
func (t *TV) SendText(text string) error {
	if len(text) > maxTextLen {
		return fmt.Errorf("samsung %s: text exceeds %d bytes", t.id, maxTextLen)
	}
	return t.sendText(text)
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
			t.clearTextInput() // launching an app takes focus from any text field
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
				t.clearTextInput()
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
// powers down, which would otherwise flicker the state.
//
// While the TV is reachable, Poll also reads the real volume and mute over UPnP
// (so changes made with the physical remote land in the UI within a tick) and
// keeps the event socket connected (so IME events stream in).
func (t *TV) Poll() (device.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*t.timeout)
	defer cancel()

	_, err := t.resolveIP()
	known := err == nil
	reachable := known && t.reachable(ctx)

	vol, haveVol := 0, false
	muted, haveMute := false, false
	if reachable {
		if v, verr := t.upnpVolume(ctx); verr == nil {
			vol, haveVol = v, true
		}
		if m, merr := t.upnpMute(ctx); merr == nil {
			muted, haveMute = m, true
		}
		t.ensureEvents(ctx)
	}

	intended, fresh := t.intendedPower()
	t.updateState(func(s *device.State) {
		s.Online = known
		if fresh {
			s.On = intended // just commanded — hold it through the power transition
		} else {
			s.On = reachable // live signal: reachable ⇒ on, unreachable ⇒ off
		}
		if haveVol {
			s.Volume = vol
		}
		if haveMute {
			s.Muted = muted
		}
		if !s.On {
			s.TextActive = false // an off TV has no focused input field
			s.TextValue = ""
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
