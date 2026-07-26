package atomberg

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"setu/internal/device"
	"setu/internal/resolver"
)

// Two different packet shapes share port 5625, so every datagram is classified
// before it is parsed:
//
//	beacon — short ASCII "<mac12>_<SERIES>", sent once a second by every fan.
//	         It is how a fan is found and how it proves it is still alive.
//	state  — the hex encoding of a JSON object, broadcast after any change
//	         (including one made with the physical remote or the vendor app).
//
// Hex-decoding is tried first: a beacon is not valid hex (it contains '_'), so
// the discrimination is unambiguous rather than length-based.

// maxBeaconLen bounds what may be treated as a beacon. A beacon is
// "<12 hex>_<series>", so anything longer is a malformed or foreign datagram
// rather than something to guess at.
const maxBeaconLen = 24

// beacon is one fan announcing itself: its MAC (canonical colon form) and the
// series string that decides which model driver can drive it.
type beacon struct {
	MAC    string
	Series string
}

// stateMessage is the decoded JSON of a state broadcast. MessageID lets a
// receiver drop retransmits of a state it has already applied.
type stateMessage struct {
	DeviceID    string `json:"device_id"`
	MessageID   string `json:"message_id"`
	StateString string `json:"state_string"`
}

// stripProxy removes the PROXY-protocol preamble some networks prepend, leaving
// the original payload. Datagrams without one are returned unchanged.
func stripProxy(datagram []byte) []byte {
	if !strings.HasPrefix(string(datagram), "PROXY ") {
		return datagram
	}
	// "PROXY TCP4 <src> <dst> <dport> <sport> <payload>" — six space-separated
	// header fields, then the payload, which may itself contain spaces.
	parts := strings.SplitN(string(datagram), " ", 7)
	if len(parts) < 7 {
		return nil
	}
	return []byte(parts[6])
}

// parseBeacon reads "<mac12>_<SERIES>". It returns false for anything that is
// not exactly that shape: a fan that is not announced is better than a fan
// invented from a stray datagram (the Scanner contract).
func parseBeacon(payload []byte) (beacon, bool) {
	text := strings.TrimSpace(string(payload))
	if text == "" || len(text) > maxBeaconLen {
		return beacon{}, false
	}
	mac, series, found := strings.Cut(text, "_")
	if !found || series == "" {
		return beacon{}, false
	}
	normalized, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return beacon{}, false
	}
	return beacon{MAC: resolver.FormatMAC(normalized), Series: strings.ToUpper(series)}, true
}

// parseState decodes a hex-encoded state broadcast. The bool reports whether
// the datagram was a state message at all; an error means it was one and was
// malformed, which is worth logging rather than silently ignoring.
func parseState(payload []byte) (stateMessage, bool, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		return stateMessage{}, false, nil // not hex: this is a beacon
	}
	var msg stateMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return stateMessage{}, true, fmt.Errorf("atomberg: state payload is not JSON: %w", err)
	}
	if msg.DeviceID == "" || msg.StateString == "" {
		return stateMessage{}, true, fmt.Errorf("atomberg: state payload is missing device_id or state_string")
	}
	return msg, true, nil
}

// Bit masks for the first field of state_string, taken from Atomberg's own
// developer documentation. The field is a DECIMAL integer (not hex) whose bits
// carry the whole user-visible state of the fan.
const (
	maskSpeed       = 0x07
	maskCool        = 0x08
	maskPower       = 0x10
	maskLED         = 0x20
	maskSleep       = 0x80
	maskBrightness  = 0x7F00
	maskWarm        = 0x8000
	maskTimerHours  = 0x0F0000
	shiftBrightness = 8
	shiftTimerHours = 16
	// Elapsed time is reported in 4-minute ticks in the top byte.
	shiftElapsed   = 24
	elapsedMinutes = 4
)

// fanState is everything one state broadcast says about a fan. It is kept
// separate from device.State because a model without a light must not publish
// the light fields at all — decode once, then let each model project the parts
// its hardware actually has.
type fanState struct {
	Power      bool
	Speed      int
	Sleep      bool
	LED        bool
	Brightness int
	LightMode  string // "warm", "cool", "daylight", or "" when not reported
	TimerHours int
	Elapsed    int
	Series     string
}

// Light modes, as the fan names them on the wire.
const (
	lightWarm     = "warm"
	lightCool     = "cool"
	lightDaylight = "daylight"
)

// seriesField is the index of the series within state_string. The remaining
// fields are undocumented vendor diagnostics and are deliberately ignored.
const seriesField = 7

// decodeStateString turns a state_string into the fan's state. Only field 0
// (the bitfield) and field 7 (the series) are read.
func decodeStateString(stateString string) (fanState, error) {
	fields := strings.Split(stateString, ",")
	if len(fields) == 0 || fields[0] == "" {
		return fanState{}, fmt.Errorf("atomberg: empty state_string")
	}
	// Parsed as an unsigned 32-bit value because that is exactly what the
	// bitfield is — the masks reach 0xFF000000 and no further. It also keeps
	// every shift below safe on a 32-bit build (Setu's deploy target), where a
	// wider number would silently wrap when converted to int.
	value, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 32)
	if err != nil {
		return fanState{}, fmt.Errorf("atomberg: state_string field 0 is not a 32-bit number: %w", err)
	}

	s := fanState{
		Power:      value&maskPower != 0,
		Speed:      int(value & maskSpeed),
		Sleep:      value&maskSleep != 0,
		LED:        value&maskLED != 0,
		Brightness: int(value&maskBrightness) >> shiftBrightness,
		TimerHours: int(value&maskTimerHours) >> shiftTimerHours,
		Elapsed:    int(value>>shiftElapsed) * elapsedMinutes,
	}
	// The two colour bits are a 2-bit enum, not two independent flags: both set
	// means daylight. Neither set means the model has no colour control, so the
	// mode stays empty rather than being reported as a mode the fan lacks.
	cool, warm := value&maskCool != 0, value&maskWarm != 0
	switch {
	case cool && warm:
		s.LightMode = lightDaylight
	case cool:
		s.LightMode = lightCool
	case warm:
		s.LightMode = lightWarm
	}
	if len(fields) > seriesField {
		s.Series = strings.ToUpper(strings.TrimSpace(fields[seriesField]))
	}
	return s, nil
}

// lightSupport says what a model's light can do. The state bits are identical
// across models, so only this decides how they are projected: reporting a level
// for a lamp that has no levels would make the UI offer a dimmer that does
// nothing.
type lightSupport int

const (
	lightNone     lightSupport = iota // no light at all
	lightOnOff                        // switches, does not dim
	lightDimmable                     // dims, and has colour modes
)

// deviceState projects a decoded fanState onto Setu's shared state shape.
func (s fanState) deviceState(light lightSupport) device.State {
	out := device.State{
		Online:           true,
		On:               s.Power,
		Speed:            s.Speed,
		Sleep:            s.Sleep,
		TimerHours:       s.TimerHours,
		TimerElapsedMins: s.Elapsed,
	}
	switch light {
	case lightOnOff:
		out.Light = s.LED
	case lightDimmable:
		// A dimmable light rides on Brightness, where 0 means off — so an unlit
		// fan reports 0 regardless of the level the hardware remembers for next
		// time.
		if s.LED {
			out.Brightness = s.Brightness
		}
		out.Scene = sceneForMode(s.LightMode)
	}
	return out
}
