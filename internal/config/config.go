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
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration, loaded from a YAML file. YAML is used
// (rather than stdlib JSON) for two reasons that matter for a hand-edited file:
// inline comments and human-friendly durations like "5s". gopkg.in/yaml.v3 is
// the single dependency this buys, and it is small and ubiquitous.
type Config struct {
	// Listen is the server address: ":8080" for TCP, or "unix:/run/setu.sock"
	// for a Unix-domain socket (tunnel-only, zero open ports).
	Listen string `yaml:"listen"`
	// Auth holds the bearer token required on /api and /ws.
	Auth AuthConfig `yaml:"auth"`
	// PollInterval is how often the state poller re-reads device state.
	PollInterval Duration `yaml:"poll_interval"`
	// Devices is the list of configured devices (empty until devices are added).
	Devices []DeviceSpec `yaml:"devices"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	Token string `yaml:"token"`
}

// DeviceSpec is one device entry from config: pure instance data. The Factory
// maps (Brand, Model) to the Go type that implements it.
type DeviceSpec struct {
	ID    string `yaml:"id"`    // stable, unique instance id
	Brand string `yaml:"brand"` // selects the device package, e.g. "wiz"
	Model string `yaml:"model"` // selects the model within the brand
	Name  string `yaml:"name"`  // human-friendly label
	MAC   string `yaml:"mac"`   // PRIMARY identity (stable across DHCP leases)
	IP    string `yaml:"ip"`    // optional hint/fallback only
}

const (
	defaultListen       = ":8080"
	defaultPollInterval = 5 * time.Second
)

// Load reads and parses the config file at path, applying defaults first so a
// sparse file still works, then validating.
func Load(path string) (*Config, error) {
	c := &Config{
		Listen:       defaultListen,
		PollInterval: Duration(defaultPollInterval),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
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
