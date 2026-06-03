package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestARPResolverLookup(t *testing.T) {
	// A fixture in the exact format of /proc/net/arp (header + rows). The second
	// row is an incomplete entry (all-zero MAC) that must never match.
	const table = `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.50     0x1         0x2         a8:bb:50:11:22:33     *        br-lan
192.168.1.99     0x1         0x0         00:00:00:00:00:00     *        br-lan
`
	path := filepath.Join(t.TempDir(), "arp")
	if err := os.WriteFile(path, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	old := arpTablePath
	arpTablePath = path
	t.Cleanup(func() { arpTablePath = old })

	r := NewARPResolver()

	// Lookup is case- and format-insensitive (normalized via net.ParseMAC).
	ip, err := r.Lookup("A8:BB:50:11:22:33")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := ip.String(); got != "192.168.1.50" {
		t.Errorf("got ip %s, want 192.168.1.50", got)
	}

	if _, err := r.Lookup("de:ad:be:ef:00:01"); err == nil {
		t.Error("expected error for unknown mac")
	}
	if _, err := r.Lookup("not-a-mac"); err == nil {
		t.Error("expected error for invalid mac")
	}
}
