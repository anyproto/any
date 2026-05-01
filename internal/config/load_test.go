package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_DefaultsOnly(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Addr != "127.0.0.1:7001" {
		t.Errorf("default addr = %q", cfg.Listen.Addr)
	}
	if cfg.Storage.Topology != "shared" {
		t.Errorf("default topology = %q", cfg.Storage.Topology)
	}
}

func TestLoad_FilePopulates(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
listen:
  addr: 127.0.0.1:9999
log:
  defaultLevel: warn
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Addr != "127.0.0.1:9999" {
		t.Errorf("listen.addr = %q", cfg.Listen.Addr)
	}
	if cfg.Log.DefaultLevel != "warn" {
		t.Errorf("log.defaultLevel = %q", cfg.Log.DefaultLevel)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: {addr: 127.0.0.1:1111}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANY_LISTEN_ADDR", "127.0.0.1:2222")
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Addr != "127.0.0.1:2222" {
		t.Errorf("env should win: got %q", cfg.Listen.Addr)
	}
}

func TestLoad_FlagOverridesEnv(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ANY_LISTEN_ADDR", "127.0.0.1:2222")
	cfg, err := Load(Flags{Addr: "127.0.0.1:3333"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Addr != "127.0.0.1:3333" {
		t.Errorf("flag should win: got %q", cfg.Listen.Addr)
	}
}

func TestLoad_EnvLogLevel(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ANY_LOG_LEVEL", "debug")
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.DefaultLevel != "debug" {
		t.Errorf("ANY_LOG_LEVEL should override Log.DefaultLevel; got %q", cfg.Log.DefaultLevel)
	}
}

func TestLoad_MissingConfigIsNotError(t *testing.T) {
	isolateEnv(t)
	_, err := Load(Flags{ConfigPath: ""})
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
}

// isolateEnv unsets every ANY_* variable and XDG_CONFIG_HOME/HOME for the
// duration of the test so file-search and env-override paths don't pick up
// the developer's real environment.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANY_DATA_DIR", "ANY_LISTEN_ADDR", "ANY_WALLET_PATH", "ANY_LOG_LEVEL",
		"ANY_WALLET_PASSKEY", "XDG_CONFIG_HOME",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// Point HOME at a tmp dir so ConfigSearchPaths doesn't find a real file.
	t.Setenv("HOME", t.TempDir())
}
