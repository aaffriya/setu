package wol

import (
	"bytes"
	"testing"
)

func TestMagicPacketFormat(t *testing.T) {
	packet, err := magicPacket("AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 102 {
		t.Fatalf("packet length = %d, want 102", len(packet))
	}
	if !bytes.Equal(packet[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatal("packet is missing the six-byte sync stream")
	}
	wantMAC := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	for offset := 6; offset < len(packet); offset += len(wantMAC) {
		if !bytes.Equal(packet[offset:offset+len(wantMAC)], wantMAC) {
			t.Fatalf("MAC copy at byte %d is malformed", offset)
		}
	}
}

func TestMagicPacketRejectsInvalidMAC(t *testing.T) {
	if _, err := magicPacket("not-a-mac"); err == nil {
		t.Fatal("invalid MAC was accepted")
	}
}
