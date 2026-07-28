package samsung

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"setu/internal/config"
	"setu/internal/device"
)

type sequenceResolver struct {
	ips   []net.IP
	calls int
}

func (r *sequenceResolver) Lookup(string) (net.IP, error) {
	if r.calls >= len(r.ips) {
		return nil, fmt.Errorf("no result")
	}
	ip := r.ips[r.calls]
	r.calls++
	return append(net.IP(nil), ip...), nil
}

func TestResolveIPCachesAndReresolvesAfterInvalidation(t *testing.T) {
	discovery := &sequenceResolver{ips: []net.IP{
		net.ParseIP("192.168.1.10"),
		net.ParseIP("192.168.1.11"),
	}}
	b := base{mac: "a0:d7:f3:9e:74:b2", discoverer: discovery}

	first, err := b.resolveIP()
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	cached, err := b.resolveIP()
	if err != nil {
		t.Fatalf("cached resolve: %v", err)
	}
	if !cached.Equal(first) || discovery.calls != 1 {
		t.Fatalf("cached resolve = %v, calls = %d; want %v, 1", cached, discovery.calls, first)
	}

	b.invalidateIP()
	second, err := b.resolveIP()
	if err != nil {
		t.Fatalf("resolve after invalidation: %v", err)
	}
	if !second.Equal(net.ParseIP("192.168.1.11")) || discovery.calls != 2 {
		t.Fatalf("resolve after invalidation = %v, calls = %d; want 192.168.1.11, 2", second, discovery.calls)
	}
}

func TestPollReportsMissingLiveResponseWithoutDisablingWake(t *testing.T) {
	tv := &TV{base: base{
		id:    "tv",
		mac:   "a0:d7:f3:9e:74:b2",
		state: device.State{Online: true, On: true},
	}}

	state, err := tv.Poll()
	if !errors.Is(err, device.ErrPollNoResponse) {
		t.Fatalf("Poll() error = %v; want ErrPollNoResponse", err)
	}
	if !state.Online {
		t.Fatal("missing live response disabled MAC-based wake availability")
	}
	if state.On {
		t.Fatal("missing live response did not clear stale power state")
	}
}

func TestDiscovererVerifiesCandidateMAC(t *testing.T) {
	const tvMAC = "A0:D7:F3:9E:74:B2"

	for _, tc := range []struct {
		name    string
		wantMAC string
		wantErr bool
	}{
		{name: "matching TV", wantMAC: "a0:d7:f3:9e:74:b2"},
		{name: "different TV", wantMAC: "00:11:22:33:44:55", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/" {
					t.Errorf("path = %q; want /api/v2/", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"device":{"wifiMac":%q}}`, tvMAC)
			}))
			defer info.Close()

			infoURL, err := url.Parse(info.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, infoPort, err := net.SplitHostPort(infoURL.Host)
			if err != nil {
				t.Fatal(err)
			}

			ssdp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer ssdp.Close()

			serverErr := make(chan error, 1)
			go func() {
				buf := make([]byte, 2048)
				n, client, err := ssdp.ReadFromUDP(buf)
				if err != nil {
					serverErr <- err
					return
				}
				if !strings.Contains(string(buf[:n]), samsungDialSearchTarget) {
					serverErr <- fmt.Errorf("search target missing from request")
					return
				}
				response := "HTTP/1.1 200 OK\r\nST: " + samsungDialSearchTarget + "\r\n\r\n"
				_, err = ssdp.WriteToUDP([]byte(response), client)
				serverErr <- err
			}()

			d := &Discoverer{
				timeout:    200 * time.Millisecond,
				searchAddr: ssdp.LocalAddr().(*net.UDPAddr),
				restPort:   infoPort,
				http:       info.Client(),
			}
			ip, err := d.Lookup(tc.wantMAC)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Lookup() = %v, nil; want error", ip)
				}
			} else {
				if err != nil {
					t.Fatalf("Lookup(): %v", err)
				}
				if !ip.Equal(net.IPv4(127, 0, 0, 1)) {
					t.Fatalf("Lookup() = %v; want 127.0.0.1", ip)
				}
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Scan feeds the "add a device" flow, so it has to carry the labels that make a
// config entry recognisable — not just the MAC that resolution needs.
func TestScanReportsTVIdentity(t *testing.T) {
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device":{"wifiMac":"A0:D7:F3:9E:74:B2","name":"[TV] Living Room","modelName":"UE43AU7700"}}`)
	}))
	defer info.Close()

	infoURL, err := url.Parse(info.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, infoPort, err := net.SplitHostPort(infoURL.Host)
	if err != nil {
		t.Fatal(err)
	}

	ssdp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ssdp.Close()
	go func() {
		buf := make([]byte, 2048)
		n, client, err := ssdp.ReadFromUDP(buf)
		if err != nil || !strings.Contains(string(buf[:n]), samsungDialSearchTarget) {
			return
		}
		// Two responses from the same TV: the scan must list it once.
		response := []byte("HTTP/1.1 200 OK\r\nST: " + samsungDialSearchTarget + "\r\n\r\n")
		_, _ = ssdp.WriteToUDP(response, client)
		_, _ = ssdp.WriteToUDP(response, client)
	}()

	d := &Discoverer{
		timeout:    300 * time.Millisecond,
		searchAddr: ssdp.LocalAddr().(*net.UDPAddr),
		restPort:   infoPort,
		http:       info.Client(),
	}

	found, err := d.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan(): %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("Scan() = %+v; want one TV", found)
	}
	got := found[0]
	if got.Brand != Brand || got.Driver != DriverTizen {
		t.Errorf("candidate driver = %s/%s; want %s/%s", got.Brand, got.Driver, Brand, DriverTizen)
	}
	if got.MAC != "a0:d7:f3:9e:74:b2" || got.Name != "[TV] Living Room" || got.Model != "UE43AU7700" {
		t.Errorf("candidate = %+v; want the TV's own mac, name and model name", got)
	}
}

func TestLiveMACOnlyDiscoveryAfterCacheClear(t *testing.T) {
	mac := os.Getenv("SETU_LIVE_SAMSUNG_MAC")
	if mac == "" {
		t.Skip("set SETU_LIVE_SAMSUNG_MAC to run against a TV on the local network")
	}

	dev, err := New(config.DeviceSpec{
		ID:    "live_mac_only_test",
		Brand: Brand,
		Model: DriverTizen,
		Name:  "Live Samsung TV",
		MAC:   mac,
	}, config.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	tv := dev.(*TV)

	first, err := tv.resolveIP()
	if err != nil {
		t.Fatalf("cold MAC-only resolve: %v", err)
	}
	if cached, err := tv.resolveIP(); err != nil || !cached.Equal(first) {
		t.Fatalf("cached resolve = %v, %v; want %v, nil", cached, err, first)
	}

	tv.invalidateIP()
	second, err := tv.resolveIP()
	if err != nil {
		t.Fatalf("resolve after cache clear: %v", err)
	}
	if !second.Equal(first) {
		t.Fatalf("resolve after cache clear = %v; want %v", second, first)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	power, reachable := tv.powerState(ctx)
	if !reachable {
		t.Fatalf("resolved TV %v did not answer /api/v2/", second)
	}
	t.Logf("MAC-only cold resolve=%v cache-cleared resolve=%v power=%s", first, second, power)
}
