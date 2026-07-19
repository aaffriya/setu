// Package config defines Setu's configuration schema and loader, plus the
// (brand, model) device Factory that turns config entries into live devices.
//
// Configuration is data, not behaviour (principle 4): an entry supplies only
// instance data (id, name, mac, …). The mapping from a (brand, model) pair to a
// concrete Go type lives in code — each device package registers its
// constructor with a Factory at startup (see cmd/setu/main.go and factory.go).
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration, loaded from a YAML file. YAML is used
// (rather than stdlib JSON) for two reasons that matter for a hand-edited file:
// inline comments and human-friendly durations like "5s". gopkg.in/yaml.v3 is
// the single dependency this buys, and it is small and ubiquitous.
type Config struct {
	// Listen configures the HTTP listener.
	Listen ListenConfig `yaml:"listen"`
	// Auth holds the bearer token required on /api and /ws.
	Auth AuthConfig `yaml:"auth"`
	// PollInterval is the active state-poll cadence; idle polling backs off.
	PollInterval Duration `yaml:"poll_interval"`
	// Devices is the list of configured devices (empty until devices are added).
	Devices []DeviceSpec `yaml:"devices"`
}

// ListenConfig describes where the server listens. By default it binds to all
// network interfaces on port 80.
type ListenConfig struct {
	// Interface is the address to bind to — the IP of the network interface,
	// e.g. "192.168.1.10". Blank means all interfaces.
	Interface string `yaml:"interface"`
	// Port is the TCP port to listen on. Defaults to 80.
	Port int `yaml:"port"`
	// Socket, when set, serves on this Unix-domain socket instead of TCP
	// (tunnel-only, zero open ports). Takes precedence over Interface/Port.
	Socket string `yaml:"socket"`
	// TLS optionally serves HTTPS with your own certificate. Leave it unset to
	// serve plain HTTP exactly as before.
	TLS TLSConfig `yaml:"tls"`
}

// TLSConfig holds an optional own/self-signed certificate. When both Cert and
// Key are set, the listener is wrapped with TLS (HTTPS) — needed so browsers
// treat http://<lan-ip> as a secure context and allow PWA install / service
// workers. Empty = plain HTTP (the default). No ACME/Let's Encrypt: bring your
// own cert (or use Tailscale for zero-config HTTPS).
type TLSConfig struct {
	Cert string `yaml:"cert"` // PEM certificate file
	Key  string `yaml:"key"`  // PEM private-key file
}

// Enabled reports whether TLS should be served (both files configured).
func (t TLSConfig) Enabled() bool {
	return t.Cert != "" && t.Key != ""
}

// Network returns the network and address for net.Listen: a Unix-domain socket
// when Socket is set, otherwise a TCP "host:port" (blank host = all interfaces).
func (l ListenConfig) Network() (network, address string) {
	if l.Socket != "" {
		return "unix", l.Socket
	}
	return "tcp", net.JoinHostPort(l.Interface, strconv.Itoa(l.Port))
}

// String renders the listener for logs, e.g. ":80", "192.168.1.10:80", or
// "unix:/run/setu.sock".
func (l ListenConfig) String() string {
	network, address := l.Network()
	if network == "unix" {
		return "unix:" + address
	}
	return address
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	Token string `yaml:"token"`
}

// DeviceSpec is one device entry from config: pure instance data. The Factory
// maps (Brand, Model) to the Go type that implements it.
type DeviceSpec struct {
	ID     string `yaml:"id"`     // stable, unique instance id
	Brand  string `yaml:"brand"`  // selects the device package, e.g. "wiz"
	Model  string `yaml:"model"`  // selects the model within the brand
	Series string `yaml:"series"` // optional friendly product/series name shown in the UI (e.g. "AU7700")
	Name   string `yaml:"name"`   // human-friendly label
	MAC    string `yaml:"mac"`    // PRIMARY identity (stable across DHCP leases)
	IP     string `yaml:"ip"`     // optional hint/fallback only
}

const (
	defaultPort         = 80
	defaultPollInterval = 45 * time.Second
)

// Load reads and parses the config file at path, applying defaults first so a
// sparse file still works, then validating.
func Load(path string) (*Config, error) {
	c := &Config{
		Listen:       ListenConfig{Port: defaultPort},
		PollInterval: Duration(defaultPollInterval),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// A listen block that omits the port falls back to the default.
	if c.Listen.Socket == "" && c.Listen.Port == 0 {
		c.Listen.Port = defaultPort
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate catches configuration mistakes at startup rather than as confusing
// runtime failures, and enforces unique device ids.
func (c *Config) validate() error {
	if c.Auth.Token == "" {
		return fmt.Errorf("config: auth.token must be set")
	}
	if c.Listen.Socket == "" && (c.Listen.Port < 1 || c.Listen.Port > 65535) {
		return fmt.Errorf("config: listen.port %d out of range (1-65535)", c.Listen.Port)
	}
	if (c.Listen.TLS.Cert == "") != (c.Listen.TLS.Key == "") {
		return fmt.Errorf("config: listen.tls needs both cert and key (or neither)")
	}
	seen := make(map[string]struct{}, len(c.Devices))
	for i, d := range c.Devices {
		if d.ID == "" {
			return fmt.Errorf("config: devices[%d]: id is required", i)
		}
		if _, dup := seen[d.ID]; dup {
			return fmt.Errorf("config: duplicate device id %q", d.ID)
		}
		seen[d.ID] = struct{}{}
		if d.Brand == "" || d.Model == "" {
			return fmt.Errorf("config: device %q: brand and model are required", d.ID)
		}
		if d.MAC == "" {
			return fmt.Errorf("config: device %q: mac is required (primary identity)", d.ID)
		}
	}
	return nil
}

// Duration is a time.Duration that unmarshals from a YAML string like "5s".
// time.Duration itself can't be decoded from such a string, so we wrap it.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a duration string such as "5s" or "200ms".
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf(`config: poll_interval must be a duration string like "5s": %w`, err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}
