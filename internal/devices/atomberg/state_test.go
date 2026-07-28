package atomberg

import (
	"encoding/hex"
	"testing"

	"setu/internal/device"
)

func TestDecodeStateString(t *testing.T) {
	// 20 is the value in Atomberg's own worked example: power on, speed 4,
	// LED off. The rest exercise each mask independently so a shifted constant
	// cannot hide behind another field.
	tests := []struct {
		name  string
		input string
		want  fanState
	}{
		{
			name:  "vendor example: on at speed 4",
			input: "20,1,B,5,50.00,0,0,R1,2802,1,45142,0,0,0,0,0.00,0.00,0,0,0,END",
			want:  fanState{Power: true, Speed: 4, Model: "R1"},
		},
		{
			name:  "off",
			input: "0,1,B,5,50.00,0,0,R1,END",
			want:  fanState{Model: "R1"},
		},
		{
			name:  "speed 6 is the boost step",
			input: "22,1,B,5,50.00,0,0,R1,END",
			want:  fanState{Power: true, Speed: 6, Model: "R1"},
		},
		{
			name:  "led on",
			input: "48,1,B,5,50.00,0,0,R1,END", // 0x30 = power|led
			want:  fanState{Power: true, LED: true, Model: "R1"},
		},
		{
			name:  "sleep on",
			input: "144,1,B,5,50.00,0,0,R1,END", // 0x90 = power|sleep
			want:  fanState{Power: true, Sleep: true, Model: "R1"},
		},
		{
			name:  "brightness 100 with a warm light",
			input: "58416,1,B,5,50.00,0,0,I1,END", // 0xE430 = power|led|warm|brightness 100
			want: fanState{
				Power: true, LED: true, Brightness: 100,
				LightMode: lightWarm, Model: "I1",
			},
		},
		{
			name:  "cool light",
			input: "56,1,B,5,50.00,0,0,I1,END", // 0x38 = power|led|cool
			want: fanState{
				Power: true, LED: true, LightMode: lightCool, Model: "I1",
			},
		},
		{
			name:  "cool and warm together mean daylight",
			input: "32824,1,B,5,50.00,0,0,I1,END", // 0x8038
			want: fanState{
				Power: true, LED: true, LightMode: lightDaylight, Model: "I1",
			},
		},
		{
			name:  "six-hour timer reads back as hours, not the command index",
			input: "393232,1,B,5,50.00,0,0,R1,END", // 0x60010
			want:  fanState{Power: true, TimerHours: 6, Model: "R1"},
		},
		{
			name:  "elapsed minutes are four-minute ticks in the top byte",
			input: "83886096,1,B,5,50.00,0,0,R1,END", // 0x05000010
			want:  fanState{Power: true, Elapsed: 20, Model: "R1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeStateString(tc.input)
			if err != nil {
				t.Fatalf("decodeStateString: %v", err)
			}
			if got != tc.want {
				t.Errorf("decoded\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestDecodeStateStringRejectsGarbage(t *testing.T) {
	for _, input := range []string{"", "not-a-number,1,B", ","} {
		if _, err := decodeStateString(input); err == nil {
			t.Errorf("decodeStateString(%q) should have failed", input)
		}
	}
}

func TestFanStateToDeviceState(t *testing.T) {
	full := fanState{
		Power: true, Speed: 3, Sleep: true, LED: true,
		Brightness: 60, LightMode: lightCool, TimerHours: 2, Elapsed: 8,
	}

	t.Run("dimmable light", func(t *testing.T) {
		got := full.deviceState(lightDimmable)
		want := device.State{
			Online: true, On: true, Speed: 3, Sleep: true,
			TimerHours: 2, TimerElapsedMins: 8,
			Brightness: 60, Scene: sceneCool,
		}
		if got != want {
			t.Errorf("\n got %+v\nwant %+v", got, want)
		}
	})

	// An on/off light reports Light, never Brightness: a level for a lamp with
	// no levels would make the UI offer a dimmer that does nothing.
	t.Run("on/off light", func(t *testing.T) {
		got := full.deviceState(lightOnOff)
		want := device.State{
			Online: true, On: true, Speed: 3, Sleep: true,
			TimerHours: 2, TimerElapsedMins: 8, Light: true,
		}
		if got != want {
			t.Errorf("\n got %+v\nwant %+v", got, want)
		}
	})

	t.Run("no light at all", func(t *testing.T) {
		got := full.deviceState(lightNone)
		if got.Brightness != 0 || got.Scene != 0 || got.Light {
			t.Errorf("light fields leaked into a light-less fan: %+v", got)
		}
		if got.Speed != 3 || !got.On {
			t.Errorf("fan fields lost: %+v", got)
		}
	})

	t.Run("an unlit dimmable light reports brightness 0", func(t *testing.T) {
		unlit := full
		unlit.LED = false
		if got := unlit.deviceState(lightDimmable); got.Brightness != 0 {
			t.Errorf("brightness = %d, want 0 when the light is off", got.Brightness)
		}
	})

	t.Run("an unlit on/off light reports Light false", func(t *testing.T) {
		unlit := full
		unlit.LED = false
		if got := unlit.deviceState(lightOnOff); got.Light {
			t.Error("Light stayed true with the lamp off")
		}
	})
}

func TestParseBeacon(t *testing.T) {
	t.Run("accepts a well-formed beacon", func(t *testing.T) {
		got, ok := parseBeacon([]byte("a0764e5aee98_R1"))
		if !ok {
			t.Fatal("parseBeacon rejected a valid beacon")
		}
		if got.MAC != "a0:76:4e:5a:ee:98" {
			t.Errorf("MAC = %q", got.MAC)
		}
		if got.Model != "R1" {
			t.Errorf("Model = %q", got.Model)
		}
	})

	t.Run("lower-case model code is normalised", func(t *testing.T) {
		got, ok := parseBeacon([]byte("a0764e5aee98_i1"))
		if !ok || got.Model != "I1" {
			t.Errorf("got %+v ok=%v", got, ok)
		}
	})

	// A scanner must never invent a device it did not hear from, so anything
	// that is not exactly a beacon has to be refused rather than guessed at.
	t.Run("rejects malformed payloads", func(t *testing.T) {
		for _, input := range []string{
			"", "a0764e5aee98", "notamac_R1", "a0764e5aee98_",
			"a0764e5aee98_R1_and_a_lot_more_text_than_a_beacon",
		} {
			if _, ok := parseBeacon([]byte(input)); ok {
				t.Errorf("parseBeacon(%q) should have been rejected", input)
			}
		}
	})
}

func TestParseState(t *testing.T) {
	payload := `{"device_id":"a0764e5aee98","message_id":"abc123",` +
		`"state_string":"20,1,B,5,50.00,0,0,R1,END"}`
	encoded := []byte(hex.EncodeToString([]byte(payload)))

	t.Run("decodes hex-encoded JSON", func(t *testing.T) {
		msg, isState, err := parseState(encoded)
		if err != nil || !isState {
			t.Fatalf("isState=%v err=%v", isState, err)
		}
		if msg.DeviceID != "a0764e5aee98" || msg.MessageID != "abc123" {
			t.Errorf("got %+v", msg)
		}
	})

	// A beacon shares the port with state messages, so the classifier must send
	// it down the other path rather than reporting an error.
	t.Run("a beacon is not a state message", func(t *testing.T) {
		_, isState, err := parseState([]byte("a0764e5aee98_R1"))
		if isState || err != nil {
			t.Errorf("isState=%v err=%v, want false/nil", isState, err)
		}
	})

	t.Run("hex that is not the expected JSON is an error", func(t *testing.T) {
		_, isState, err := parseState([]byte(hex.EncodeToString([]byte(`{"a":1}`))))
		if !isState || err == nil {
			t.Errorf("isState=%v err=%v, want true/non-nil", isState, err)
		}
	})
}

func TestStripProxy(t *testing.T) {
	got := stripProxy([]byte("PROXY TCP4 10.0.0.1 10.0.0.2 5625 5625 a0764e5aee98_R1"))
	if string(got) != "a0764e5aee98_R1" {
		t.Errorf("stripProxy = %q", got)
	}
	plain := []byte("a0764e5aee98_R1")
	if string(stripProxy(plain)) != string(plain) {
		t.Error("stripProxy altered a datagram with no PROXY preamble")
	}
}
