package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func soundConfig(stateDir string) (*Config, string) {
	return &Config{
		Listen:       ListenConfig{Port: 8080},
		Token:        "a-real-secret-token",
		PollInterval: 45 * time.Second,
	}, filepath.Join(stateDir, "setu.json")
}

func TestPreflightPassesASoundInstallation(t *testing.T) {
	cfg, statePath := soundConfig(t.TempDir())
	if problems := Preflight(cfg, statePath); len(problems) != 0 {
		t.Fatalf("a sound installation reported %+v", problems)
	}
}

// A state directory this process cannot write is the failure that otherwise
// shows up much later, as devices that will not save.
func TestPreflightCatchesAnUnwritableStateDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere, so there is nothing to catch")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	cfg, _ := soundConfig(dir)
	problems := Preflight(cfg, filepath.Join(dir, "setu.json"))
	if !hasFatal(problems, "cannot write devices, automations and people") {
		t.Fatalf("problems = %+v, want a fatal write failure", problems)
	}
}

// Probing must leave no files behind: the check runs on every start. The
// directory itself may be created, because the store would create it anyway.
func TestPreflightLeavesNoFilesBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	cfg, statePath := soundConfig(dir)
	if problems := Preflight(cfg, statePath); len(problems) != 0 {
		t.Fatalf("a missing state directory reported %+v", problems)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the state directory was not created: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("preflight left %d files behind", len(entries))
	}
}

// "cannot assign requested address" says nothing about which addresses would
// have worked, so the check lists them.
func TestPreflightCatchesAnAddressThisHostDoesNotHave(t *testing.T) {
	cfg, statePath := soundConfig(t.TempDir())
	cfg.Listen.Interface = "203.0.113.7"
	problems := Preflight(cfg, statePath)
	if !hasFatal(problems, "no interface on this host") {
		t.Fatalf("problems = %+v, want a fatal bind address", problems)
	}

	cfg.Listen.Interface = "not-an-ip"
	if !hasFatal(Preflight(cfg, statePath), "is not an IP address") {
		t.Fatal("a non-address was accepted")
	}

	cfg.Listen.Interface = "127.0.0.1"
	if problems := Preflight(cfg, statePath); len(problems) != 0 {
		t.Fatalf("loopback reported %+v", problems)
	}
}

func TestPreflightCatchesAnUnusableCertificate(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, statePath := soundConfig(dir)
	cfg.Listen.TLS = TLSConfig{Cert: cert, Key: key}
	if !hasFatal(Preflight(cfg, statePath), "cannot use the TLS certificate") {
		t.Fatal("an unusable certificate was accepted")
	}
}

// The default token is a warning, not a refusal: a fresh install has to be able
// to start so its owner can reach the app and change it.
func TestPreflightWarnsAboutTheDefaultToken(t *testing.T) {
	cfg, statePath := soundConfig(t.TempDir())
	cfg.Token = DefaultToken
	problems := Preflight(cfg, statePath)
	if len(problems) != 1 || problems[0].Fatal || !strings.Contains(problems[0].Message, EnvToken) {
		t.Fatalf("problems = %+v, want one warning about the token", problems)
	}
}

// Fatal findings come first: the reason Setu is about to stop should not be
// buried under advice.
func TestPreflightReportsFatalProblemsFirst(t *testing.T) {
	cfg, _ := soundConfig(t.TempDir())
	cfg.Token = DefaultToken
	cfg.Listen.Interface = "203.0.113.7"
	problems := Preflight(cfg, filepath.Join(t.TempDir(), "setu.json"))
	if len(problems) < 2 || !problems[0].Fatal || problems[len(problems)-1].Fatal {
		t.Fatalf("problems = %+v, want fatal ones first", problems)
	}
}

func hasFatal(problems []Problem, contains string) bool {
	for _, problem := range problems {
		if problem.Fatal && strings.Contains(problem.Message, contains) {
			return true
		}
	}
	return false
}
