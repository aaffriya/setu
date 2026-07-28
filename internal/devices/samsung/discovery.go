package samsung

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"setu/internal/resolver"
)

const (
	ssdpPort                = 1900
	samsungDialSearchTarget = "urn:dial-multiscreen-org:device:dialreceiver:1"
	discoveryTimeout        = 2500 * time.Millisecond
	// M-SEARCH goes out more than once inside the window: SSDP is UDP, and a TV
	// that misses the request (or whose reply is lost) is indistinguishable from
	// one that is not there.
	searchProbes = 2
)

// Discoverer finds Samsung TVs without a configured IP. It asks for DIAL
// receivers over SSDP, then reads each responder's /api/v2/ document — the step
// that separates a Samsung TV from any other DIAL device on the network and
// yields its wifiMac. Both address seams run over that one exchange:
//
//	Lookup — resolver.Resolver: keep the responder whose wifiMac is the wanted
//	         one, so a changed DHCP lease never loses a configured TV.
//	Scan   — resolver.Scanner: keep every Samsung responder, so a TV that is not
//	         added yet can be listed for the user.
type Discoverer struct {
	timeout    time.Duration
	searchAddr *net.UDPAddr
	restPort   string
	http       *http.Client
}

func NewDiscoverer(client *http.Client) *Discoverer {
	if client == nil {
		client = http.DefaultClient
	}
	return &Discoverer{
		timeout: discoveryTimeout,
		searchAddr: &net.UDPAddr{
			IP:   net.IPv4(239, 255, 255, 250),
			Port: ssdpPort,
		},
		restPort: restPort,
		http:     client,
	}
}

// Lookup implements resolver.Resolver.
func (d *Discoverer) Lookup(mac string) (net.IP, error) {
	want, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}

	var found net.IP
	err = d.search(context.Background(), func(ip net.IP, deadline time.Time) bool {
		info, err := d.deviceInfo(ip, deadline)
		if err != nil || info.MAC != want {
			return false
		}
		found = append(net.IP(nil), ip...)
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("samsung discovery: no TV with mac %s replied", want)
	}
	return found, nil
}

// Scan implements resolver.Scanner. DIAL is spoken by plenty of non-Samsung
// hardware; those responders fail the /api/v2/ read and are simply dropped, so
// the result only ever contains TVs this package can actually drive.
func (d *Discoverer) Scan(ctx context.Context) ([]resolver.Candidate, error) {
	var found []resolver.Candidate
	seen := make(map[string]struct{})
	err := d.search(ctx, func(ip net.IP, deadline time.Time) bool {
		info, err := d.deviceInfo(ip, deadline)
		if err != nil {
			return false
		}
		if _, dup := seen[info.MAC]; dup {
			return false
		}
		seen[info.MAC] = struct{}{}
		found = append(found, resolver.Candidate{
			Brand:  Brand,
			Driver: DriverTizen,
			Model:  info.Model,
			Name:   info.Name,
			MAC:    resolver.FormatMAC(info.MAC),
			IP:     ip.String(),
		})
		return false // keep listening until the window closes
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// search sends one M-SEARCH and hands each distinct DIAL responder to visit,
// until visit reports it is done, the reply window closes, or ctx ends. visit
// receives the shared deadline so its per-responder HTTP call stays inside the
// caller's overall budget.
func (d *Discoverer) search(ctx context.Context, visit func(net.IP, time.Time) bool) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return fmt.Errorf("samsung discovery: listen: %w", err)
	}
	defer conn.Close()

	request := []byte("M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 1\r\n" +
		"ST: " + samsungDialSearchTarget + "\r\n\r\n")
	if _, err := conn.WriteToUDP(request, d.searchAddr); err != nil {
		return fmt.Errorf("samsung discovery: send: %w", err)
	}

	deadline := time.Now().Add(d.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	// A cancelled caller must not keep the socket for the rest of the window:
	// an expired deadline unblocks the read immediately.
	defer context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })()

	slot := d.timeout / searchProbes
	seen := make(map[string]struct{})
	buf := make([]byte, 4096)
	for probe := 0; probe < searchProbes; probe++ {
		if ctx.Err() != nil {
			return nil // the caller gave up: no further probes
		}
		if probe > 0 {
			// A TV that missed the first M-SEARCH (or whose reply was lost)
			// gets another chance inside the same window.
			if _, err := conn.WriteToUDP(request, d.searchAddr); err != nil {
				return nil // the earlier replies still stand
			}
		}
		until := time.Now().Add(slot)
		if until.After(deadline) {
			until = deadline
		}
		_ = conn.SetReadDeadline(until)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				break // this probe's slot is over — send the next one
			}
			response := strings.ToLower(string(buf[:n]))
			if !strings.Contains(response, strings.ToLower(samsungDialSearchTarget)) {
				continue
			}
			key := addr.IP.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			if visit(addr.IP, deadline) {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return nil
		}
	}
	return nil
}

// deviceInfo is the identity a Samsung TV publishes at /api/v2/: the MAC (the
// only field that matters for resolution) plus the labels worth showing when
// offering the TV as a candidate.
type deviceInfo struct {
	MAC   string // normalized
	Name  string // user-set TV name, e.g. "[TV] Living Room"
	Model string // the model name the TV reports, e.g. "UE43AU7700"
}

func (d *Discoverer) deviceInfo(ip net.IP, deadline time.Time) (deviceInfo, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return deviceInfo{}, context.DeadlineExceeded
	}
	if remaining > time.Second {
		remaining = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()

	u := fmt.Sprintf("http://%s/api/v2/", net.JoinHostPort(ip.String(), d.restPort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return deviceInfo{}, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return deviceInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return deviceInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var info struct {
		Device struct {
			WiFiMAC   string `json:"wifiMac"`
			Name      string `json:"name"`
			ModelName string `json:"modelName"`
		} `json:"device"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return deviceInfo{}, err
	}
	mac, err := resolver.NormalizeMAC(info.Device.WiFiMAC)
	if err != nil {
		return deviceInfo{}, err
	}
	return deviceInfo{MAC: mac, Name: info.Device.Name, Model: info.Device.ModelName}, nil
}
