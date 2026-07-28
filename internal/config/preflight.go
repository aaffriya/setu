package config

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Startup self-test.
//
// Every problem here used to surface later and somewhere else: a state
// directory that is not writable looks like devices that will not save, a
// mistyped bind address looks like a server that started and cannot be reached,
// and a certificate the process cannot read fails only once the listener opens.
// Checking them at startup turns each into one sentence saying what is wrong and
// what to do, printed while the person is still watching the log.
//
// It leaves nothing behind that Setu would not create anyway: the state
// directory is created if missing — the store does that on its first write —
// and then probed with a temporary file that is removed again. The certificate
// is parsed but never served, and no port is bound.

// Problem is one finding. Fatal means Setu cannot work; otherwise it is a
// warning worth printing but not worth refusing to start over.
type Problem struct {
	Fatal   bool
	Message string
}

// Preflight checks the environment this configuration is about to run in and
// returns everything it found, fatal problems first. An empty result means the
// installation looks sound.
func Preflight(c *Config, statePath string) []Problem {
	var problems []Problem
	add := func(fatal bool, format string, args ...any) {
		problems = append(problems, Problem{Fatal: fatal, Message: fmt.Sprintf(format, args...)})
	}

	if c.Token == DefaultToken {
		add(false, "running with the default access token — set %s to a secret of your own", EnvToken)
	} else if len(c.Token) < 12 {
		add(false, "%s is short enough to guess from the LAN; use at least 12 characters", EnvToken)
	}

	if err := checkWritableDir(filepath.Dir(statePath)); err != nil {
		add(true, "cannot write devices, automations and people to %s: %v (set %s to storage this process can write)",
			filepath.Dir(statePath), err, EnvStateDir)
	}

	if c.Listen.Socket != "" {
		if err := checkWritableDir(filepath.Dir(c.Listen.Socket)); err != nil {
			add(true, "cannot create the socket %s: %v", c.Listen.Socket, err)
		}
	} else {
		if err := checkInterface(c.Listen.Interface); err != nil {
			add(true, "%s: %v", EnvInterface, err)
		}
		if c.Listen.Port < 1024 && os.Geteuid() != 0 {
			add(false, "port %d needs privilege: run as root, grant CAP_NET_BIND_SERVICE, or set %s above 1023",
				c.Listen.Port, EnvPort)
		}
	}

	if c.Listen.TLS.Enabled() {
		if _, err := tls.LoadX509KeyPair(c.Listen.TLS.Cert, c.Listen.TLS.Key); err != nil {
			add(true, "cannot use the TLS certificate: %v (check %s and %s)", err, EnvTLSCert, EnvTLSKey)
		}
	}

	if c.PollInterval < 0 {
		add(true, "%s cannot be negative", EnvPollInterval)
	} else if c.PollInterval > 0 && c.PollInterval < time.Second {
		add(false, "%s of %s will poll every device several times a second; on router-class hardware that is the whole CPU budget",
			EnvPollInterval, c.PollInterval)
	}

	// Fatal findings first: the log is read top-down, and the reason Setu is
	// about to stop should not be buried under advice.
	fatal := make([]Problem, 0, len(problems))
	warnings := make([]Problem, 0, len(problems))
	for _, problem := range problems {
		if problem.Fatal {
			fatal = append(fatal, problem)
		} else {
			warnings = append(warnings, problem)
		}
	}
	return append(fatal, warnings...)
}

// checkWritableDir verifies that a directory exists (creating it when it does
// not, which is what the store does on its first write anyway) and that this
// process can actually write a file there. Permission is checked by doing it:
// the mode bits alone say nothing about a read-only mount, a full filesystem, or
// a container's user mapping.
func checkWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".setu-preflight-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	defer os.Remove(name)
	if _, err := probe.WriteString("ok"); err != nil {
		probe.Close()
		return err
	}
	return probe.Close()
}

// checkInterface verifies that a configured bind address is one this host
// actually has. Binding an address that has moved (a DHCP lease, a renamed
// interface) fails with "cannot assign requested address", which says nothing
// about which addresses would have worked — so this lists them.
func checkInterface(address string) error {
	if address == "" {
		return nil
	}
	if net.ParseIP(address) == nil {
		return fmt.Errorf("%q is not an IP address", address)
	}
	available, err := localAddresses()
	if err != nil {
		// The host would not tell us. Let the listener be the judge rather than
		// refusing to start over a question we could not ask.
		return nil
	}
	for _, candidate := range available {
		if candidate == address {
			return nil
		}
	}
	return fmt.Errorf("no interface on this host has the address %s (available: %s)",
		address, strings.Join(available, ", "))
}

func localAddresses() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			out = append(out, ipNet.IP.String())
		}
	}
	return out, nil
}
