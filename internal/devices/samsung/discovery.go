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
)

// Discoverer resolves a Samsung TV's current IP without a configured IP. It
// asks for DIAL receivers over SSDP, then verifies each candidate against the
// TV's /api/v2/ wifiMac field. The MAC remains the source of truth even when a
// DHCP lease changes.
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

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, fmt.Errorf("samsung discovery: listen: %w", err)
	}
	defer conn.Close()

	request := []byte("M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 1\r\n" +
		"ST: " + samsungDialSearchTarget + "\r\n\r\n")
	if _, err := conn.WriteToUDP(request, d.searchAddr); err != nil {
		return nil, fmt.Errorf("samsung discovery: send: %w", err)
	}

	deadline := time.Now().Add(d.timeout)
	_ = conn.SetReadDeadline(deadline)
	seen := make(map[string]struct{})
	buf := make([]byte, 4096)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
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

		got, err := d.candidateMAC(addr.IP, deadline)
		if err == nil && got == want {
			return append(net.IP(nil), addr.IP...), nil
		}
	}
	return nil, fmt.Errorf("samsung discovery: no TV with mac %s replied", want)
}

func (d *Discoverer) candidateMAC(ip net.IP, deadline time.Time) (string, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", context.DeadlineExceeded
	}
	if remaining > time.Second {
		remaining = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()

	u := fmt.Sprintf("http://%s/api/v2/", net.JoinHostPort(ip.String(), d.restPort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var info struct {
		Device struct {
			WiFiMAC string `json:"wifiMac"`
		} `json:"device"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return "", err
	}
	return resolver.NormalizeMAC(info.Device.WiFiMAC)
}
