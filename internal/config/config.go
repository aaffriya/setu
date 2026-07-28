// Package config holds Setu's runtime settings, the device spec, and the
// (brand, driver) device Factory that turns a spec into a live device.
//
// There is no configuration file. Server settings come from the environment —
// every variable optional, each falling back to the default Setu has always
// shipped — and the devices the user added live in the state file
// (internal/store), added from the UI rather than hand-written.
//
// Configuration is still data, not behaviour (principle 4): a DeviceSpec
// supplies only instance data (id, name, mac, …). The mapping from a
// (brand, driver) pair to a concrete Go type lives in code — each device package
// registers its constructor with a Factory at startup (see cmd/setu/main.go and
// factory.go).
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"setu/internal/resolver"
)

// Environment variables. All optional; an unset variable keeps the default.
const (
	EnvToken        = "SETU_TOKEN"         // bearer token for /api and /ws
	EnvInterface    = "SETU_INTERFACE"     // bind address; blank = all interfaces
	EnvPort         = "SETU_PORT"          // TCP port; default 80, or 443 with TLS
	EnvTLSCert      = "SETU_TLS_CERT"      // PEM certificate → serves HTTPS
	EnvTLSKey       = "SETU_TLS_KEY"       // PEM private key
	EnvPollInterval = "SETU_POLL_INTERVAL" // active poll cadence, e.g. "45s"
	EnvStateDir     = "SETU_STATE_DIR"     // directory holding setu.json and pairing tokens
)

// DefaultToken is the placeholder token an untouched install runs with. The
// composition root warns while it is still in use.
const DefaultToken = "CHANGE_ME"

const (
	// Port defaults follow the scheme, the way a browser does: 80 for plain
	// HTTP, 443 once a certificate is configured. An explicit SETU_PORT always
	// wins, so this only decides what "unset" means.
	defaultPort         = 80
	defaultTLSPort      = 443
	defaultPollInterval = 45 * time.Second
)

// Config is the complete server configuration.
type Config struct {
	// Listen configures the HTTP listener.
	Listen ListenConfig
	// Token is the bearer token required on /api and /ws.
	Token string
	// PollInterval is the active state-poll cadence; idle polling backs off.
	PollInterval time.Duration
}

// ListenConfig describes where the server listens. By default it binds to all
// network interfaces on port 80.
type ListenConfig struct {
	// Interface is the address to bind to — the IP of the network interface,
	// e.g. "192.168.1.10". Blank means all interfaces.
	Interface string
	// Port is the TCP port to listen on.
	Port int
	// TLS optionally serves HTTPS with your own certificate.
	TLS TLSConfig
}

// TLSConfig holds an optional own/self-signed certificate. When both Cert and
// Key are set, the listener is wrapped with TLS (HTTPS) — needed so browsers
// treat http://<lan-ip> as a secure context and allow PWA install / service
// workers. Empty = plain HTTP (the default). No ACME/Let's Encrypt: bring your
// own cert (or use Tailscale for zero-config HTTPS).
type TLSConfig struct {
	Cert string // PEM certificate file
	Key  string // PEM private-key file
}

// Enabled reports whether TLS should be served (both files configured).
func (t TLSConfig) Enabled() bool {
	return t.Cert != "" && t.Key != ""
}

// Address returns the TCP "host:port" for net.Listen (blank host = all
// interfaces).
func (l ListenConfig) Address() string {
	return net.JoinHostPort(l.Interface, strconv.Itoa(l.Port))
}

// String renders the listener for logs, e.g. ":80" or "192.168.1.10:80".
func (l ListenConfig) String() string {
	return l.Address()
}

// Load reads the configuration from the environment, applying defaults for
// everything that is unset, then validates the result. With a completely empty
// environment it returns the shipped defaults: all interfaces, port 80, the
// placeholder token, a 45s poll cadence. Configuring TLS moves the default port
// to 443; see example.env for the full list.
func Load() (*Config, error) {
	c := &Config{
		Listen: ListenConfig{
			Interface: os.Getenv(EnvInterface),
			Port:      defaultPort,
			TLS: TLSConfig{
				Cert: os.Getenv(EnvTLSCert),
				Key:  os.Getenv(EnvTLSKey),
			},
		},
		Token:        DefaultToken,
		PollInterval: defaultPollInterval,
	}

	if raw := os.Getenv(EnvToken); raw != "" {
		c.Token = raw
	}
	if c.Listen.TLS.Enabled() {
		c.Listen.Port = defaultTLSPort
	}
	if raw := os.Getenv(EnvPort); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("config: %s must be a number, got %q", EnvPort, raw)
		}
		c.Listen.Port = port
	}
	if raw := os.Getenv(EnvPollInterval); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("config: %s must be a duration like \"45s\", got %q", EnvPollInterval, raw)
		}
		c.PollInterval = interval
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate catches mistakes at startup rather than as confusing runtime
// failures.
func (c *Config) validate() error {
	if c.Listen.Port < 1 || c.Listen.Port > 65535 {
		return fmt.Errorf("config: %s %d out of range (1-65535)", EnvPort, c.Listen.Port)
	}
	if (c.Listen.TLS.Cert == "") != (c.Listen.TLS.Key == "") {
		return fmt.Errorf("config: TLS needs both %s and %s (or neither)", EnvTLSCert, EnvTLSKey)
	}
	return nil
}

// DeviceSpec is one device the user added: pure instance data. The Factory maps
// (Brand, Driver) to the Go type that implements it. Specs are stored in the
// state file and edited through the API, never hand-written into a config file.
//
// Brand + Driver is identity and must not change after an add — a different
// driver is a different device, so editing it is a remove and an add. Name and
// Model are labels the user may edit freely.
type DeviceSpec struct {
	ID     string `json:"id"`              // stable, unique instance id
	Brand  string `json:"brand"`           // selects the device package, e.g. "WiZ"
	Driver string `json:"driver"`          // selects the driver within the brand, e.g. "color_bulb"
	Model  string `json:"model,omitempty"` // the hardware, e.g. "UE43AU7700"; blank until something reports it
	Name   string `json:"name"`            // human-friendly label
	MAC    string `json:"mac"`             // PRIMARY identity (stable across DHCP leases)
}

// Field limits. Ids also become file names (the Samsung pairing token is stored
// per device id), so they are restricted to a safe, lower-case alphabet.
const (
	MaxIDLength    = 32
	MaxNameLength  = 48
	MaxModelLength = 32
)

// Validate reports whether the spec can be built and stored. It is the single
// gate for everything that reaches the state file, whether typed by a user,
// copied from a network scan, or restored from a backup.
//
// These messages are shown to the person who just typed the value — the API
// passes them through — so they say what is wrong, without a package prefix or
// any internal vocabulary.
func (s DeviceSpec) Validate() error {
	if !validID(s.ID) {
		return fmt.Errorf("id %q must be 1-%d characters of a-z, 0-9, _ or -", s.ID, MaxIDLength)
	}
	if s.Brand == "" || s.Driver == "" {
		return fmt.Errorf("brand and driver are required")
	}
	if s.Name == "" {
		return fmt.Errorf("a name is required")
	}
	// Counted in characters, not bytes: these limits are shown to the person
	// typing, and "बैठक की लाइट" is 12 characters however many bytes it takes.
	if utf8.RuneCountInString(s.Name) > MaxNameLength {
		return fmt.Errorf("the name is longer than %d characters", MaxNameLength)
	}
	if utf8.RuneCountInString(s.Model) > MaxModelLength {
		return fmt.Errorf("the model is longer than %d characters", MaxModelLength)
	}
	if _, err := resolver.NormalizeMAC(s.MAC); err != nil {
		return fmt.Errorf("%q is not a MAC address (the device's identity)", s.MAC)
	}
	return nil
}

// Normalized returns the spec as it should be stored: trimmed labels and the
// MAC in one canonical notation, so two spellings of the same device cannot
// both be added.
func (s DeviceSpec) Normalized() DeviceSpec {
	s.ID = strings.ToLower(strings.TrimSpace(s.ID))
	s.Brand = strings.TrimSpace(s.Brand)
	s.Driver = strings.TrimSpace(s.Driver)
	s.Model = strings.TrimSpace(s.Model)
	s.Name = strings.TrimSpace(s.Name)
	s.MAC = resolver.FormatMAC(strings.TrimSpace(s.MAC))
	return s
}

// LegacyDeviceSpec is a device as format version 1 wrote it, when "model" meant
// the driver key and the hardware the device reported was filed under "series".
// It exists so an existing state file and an older exported backup still load;
// both call Upgrade and then treat the result as any other spec.
//
// Removable once no version-1 data can reach this build.
type LegacyDeviceSpec struct {
	ID     string `json:"id"`
	Brand  string `json:"brand"`
	Model  string `json:"model"`            // the driver key, in this version
	Series string `json:"series,omitempty"` // the hardware, in this version
	Name   string `json:"name"`
	MAC    string `json:"mac"`
}

// Upgrade rewrites a version-1 spec into the current one.
func (l LegacyDeviceSpec) Upgrade() DeviceSpec {
	return DeviceSpec{
		ID:     l.ID,
		Brand:  l.Brand,
		Driver: l.Model,
		Model:  l.Series,
		Name:   l.Name,
		MAC:    l.MAC,
	}
}

// UpgradeDeviceSpecs converts a whole version-1 device list.
func UpgradeDeviceSpecs(legacy []LegacyDeviceSpec) []DeviceSpec {
	specs := make([]DeviceSpec, 0, len(legacy))
	for _, l := range legacy {
		specs = append(specs, l.Upgrade())
	}
	return specs
}

func validID(id string) bool {
	if id == "" || len(id) > MaxIDLength {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '_' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}
