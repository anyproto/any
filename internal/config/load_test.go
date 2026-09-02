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

func TestLoad_AccountPrecedence(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("account: AFromFile\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Account != "AFromFile" {
		t.Errorf("file account: got %q", cfg.Account)
	}

	t.Setenv("ANY_ACCOUNT", "AFromEnv")
	if cfg, _ = Load(Flags{ConfigPath: path}); cfg.Account != "AFromEnv" {
		t.Errorf("env should win over file: got %q", cfg.Account)
	}

	if cfg, _ = Load(Flags{ConfigPath: path, Account: "AFromFlag"}); cfg.Account != "AFromFlag" {
		t.Errorf("flag should win over env: got %q", cfg.Account)
	}
}

// TestLoad_Mode pins the ownership-mode knob: standalone by default,
// file → env → flag precedence, unknown values rejected, and the
// standalone-only selectors refused under managed regardless of the
// layer they came from.
func TestLoad_Mode(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeStandalone || cfg.Managed() {
		t.Fatalf("default mode = %q, want standalone", cfg.Mode)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("mode: managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(Flags{ConfigPath: path}); err != nil || !cfg.Managed() {
		t.Fatalf("file mode: cfg=%+v err=%v", cfg.Mode, err)
	}
	t.Setenv("ANY_MODE", "standalone")
	if cfg, err = Load(Flags{ConfigPath: path}); err != nil || cfg.Managed() {
		t.Fatalf("env should win over file: mode=%q err=%v", cfg.Mode, err)
	}
	if cfg, err = Load(Flags{ConfigPath: path, Mode: "managed"}); err != nil || !cfg.Managed() {
		t.Fatalf("flag should win over env: mode=%q err=%v", cfg.Mode, err)
	}

	if _, err = Load(Flags{Mode: "hosted"}); err == nil {
		t.Error("unknown mode must be rejected")
	}
	// Managed + a standalone-only selector is contradictory.
	if _, err = Load(Flags{Mode: "managed", Account: "Azz"}); err == nil {
		t.Error("managed + account selector must be rejected")
	}
	t.Setenv("ANY_ACCOUNT", "Azz")
	if _, err = Load(Flags{Mode: "managed"}); err == nil {
		t.Error("managed + ANY_ACCOUNT must be rejected")
	}
	os.Unsetenv("ANY_ACCOUNT")
	if _, err = Load(Flags{Mode: "managed", WalletPath: "/w.key"}); err == nil {
		t.Error("managed + wallet path must be rejected")
	}
}

// TestLoadClient_SkipsModeValidation: the CLI's client side (`any
// stop`, address discovery) must reach a server by account even when
// the environment carries a managed host's ANY_MODE.
func TestLoadClient_SkipsModeValidation(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ANY_MODE", "managed")
	if _, err := Load(Flags{Account: "Azz"}); err == nil {
		t.Fatal("Load must reject managed + account")
	}
	cfg, err := LoadClient(Flags{Account: "Azz"})
	if err != nil || cfg.Account != "Azz" {
		t.Fatalf("LoadClient: cfg.Account=%q err=%v", cfg.Account, err)
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
func TestLoad_IndexDefaults(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Index.Enabled {
		t.Error("index.enabled should default true")
	}
	if cfg.Index.Embedder != "auto" {
		t.Errorf("index.embedder should default to auto, got %q", cfg.Index.Embedder)
	}
	if cfg.Index.OpenAI.Model == "" || cfg.Index.OpenAI.ApiKey == "" {
		t.Error("auto default needs baked OpenAI model + key for the online primary")
	}
}

func TestLoad_IndexFileAndEnv(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
index:
  embedder: ollama
  ollama:
    model: nomic-embed-text
  vector:
    dim: 768
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	// File populates; enabled stays at its true default when absent.
	if !cfg.Index.Enabled {
		t.Error("index.enabled should stay true when the file omits it")
	}
	if cfg.Index.Embedder != "ollama" || cfg.Index.Ollama.Model != "nomic-embed-text" || cfg.Index.Vector.Dim != 768 {
		t.Errorf("file values not applied: %+v", cfg.Index)
	}

	// Env wins over file.
	t.Setenv("ANY_INDEX_ENABLED", "false")
	t.Setenv("ANY_INDEX_EMBEDDER", "openai")
	t.Setenv("ANY_INDEX_OPENAI_API_KEY", "sk-test")
	t.Setenv("ANY_INDEX_VECTOR_DIM", "1536")
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Index.Enabled {
		t.Error("ANY_INDEX_ENABLED=false should win")
	}
	if cfg.Index.Embedder != "openai" || cfg.Index.OpenAI.ApiKey != "sk-test" || cfg.Index.Vector.Dim != 1536 {
		t.Errorf("env values not applied: %+v", cfg.Index)
	}
}

func TestLoad_IndexLocalFileAndEnv(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
index:
  embedder: local
  local:
    modelPath: /models/custom.gguf
    contextSize: 4096
    threads: 6
    gpuLayers: 0
    requestTimeout: 90s
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Index.Embedder != "local" || cfg.Index.Local.ModelPath != "/models/custom.gguf" || cfg.Index.Local.ContextSize != 4096 {
		t.Errorf("file values not applied: %+v", cfg.Index)
	}
	// The embedder-child knobs: CPU budget, forced-CPU decoding, and the
	// round-trip bound all come from config.yaml.
	if cfg.Index.Local.Threads != 6 || cfg.Index.Local.RequestTimeout != "90s" {
		t.Errorf("child knobs not applied: %+v", cfg.Index.Local)
	}
	if cfg.Index.Local.GpuLayers == nil || *cfg.Index.Local.GpuLayers != 0 {
		t.Errorf("gpuLayers not applied: %v", cfg.Index.Local.GpuLayers)
	}

	// Env wins over file.
	t.Setenv("ANY_INDEX_LOCAL_MODEL_PATH", "/models/other.gguf")
	t.Setenv("ANY_INDEX_LOCAL_LIB_DIR", "/opt/llamacpp")
	t.Setenv("ANY_INDEX_LOCAL_CONTEXT_SIZE", "1024")
	t.Setenv("ANY_INDEX_LOCAL_DIM", "512")
	t.Setenv("ANY_INDEX_LOCAL_THREADS", "3")
	t.Setenv("ANY_INDEX_LOCAL_REQUEST_TIMEOUT", "45s")
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Index.Local.ModelPath != "/models/other.gguf" || cfg.Index.Local.LibDir != "/opt/llamacpp" ||
		cfg.Index.Local.ContextSize != 1024 || cfg.Index.Local.Dim != 512 ||
		cfg.Index.Local.Threads != 3 || cfg.Index.Local.RequestTimeout != "45s" {
		t.Errorf("env values not applied: %+v", cfg.Index.Local)
	}
}

// TestLoad_PushFileAndEnv covers the push block: file populates, env
// wins over file, ANY_PUSH_ADDRS splits on commas, and the
// enabled-tristate (nil = enabled iff peerId set; explicit false wins
// even with a peer configured).
func TestLoad_PushFileAndEnv(t *testing.T) {
	isolateEnv(t)

	// Defaults: nothing configured means the production network, which
	// carries the production push node with it (see ApplyPushDefaults).
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Push.PeerId != ProdPushPeerId || !cfg.Push.Active() {
		t.Errorf("unconfigured load should carry the production push node: %+v", cfg.Push)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
push:
  peerId: 12D3KooWPush
  addrs:
    - quic://push.example:8271
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Push.PeerId != "12D3KooWPush" || len(cfg.Push.Addrs) != 1 || cfg.Push.Addrs[0] != "quic://push.example:8271" {
		t.Errorf("file values not applied: %+v", cfg.Push)
	}
	// nil tristate + peerId present → enabled and active.
	if !cfg.Push.IsEnabled() || !cfg.Push.Active() {
		t.Errorf("peerId set should imply enabled+active: %+v", cfg.Push)
	}

	// Env wins over file; addrs are comma-separated (whitespace and
	// empty entries dropped).
	t.Setenv("ANY_PUSH_PEER_ID", "12D3KooWOther")
	t.Setenv("ANY_PUSH_ADDRS", " quic://a:1 , b:2 ,")
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Push.PeerId != "12D3KooWOther" {
		t.Errorf("ANY_PUSH_PEER_ID should win: %+v", cfg.Push)
	}
	if len(cfg.Push.Addrs) != 2 || cfg.Push.Addrs[0] != "quic://a:1" || cfg.Push.Addrs[1] != "b:2" {
		t.Errorf("ANY_PUSH_ADDRS should split on commas: %#v", cfg.Push.Addrs)
	}

	// Explicit false disables even with a peer configured.
	t.Setenv("ANY_PUSH_ENABLED", "false")
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Push.IsEnabled() || cfg.Push.Active() {
		t.Errorf("ANY_PUSH_ENABLED=false should disable push: %+v", cfg.Push)
	}

	// Explicit true without peer info is enabled but can't run. Pinned
	// to a named network so the production push default stays out of the
	// way — the tristate, not the packaged pairing, is what's under test.
	t.Setenv("ANY_PUSH_ENABLED", "true")
	t.Setenv("ANY_PUSH_PEER_ID", "")
	os.Unsetenv("ANY_PUSH_PEER_ID")
	t.Setenv("ANY_PUSH_ADDRS", "")
	os.Unsetenv("ANY_PUSH_ADDRS")
	t.Setenv("ANY_NETWORK_NODECONF_PATH", "/etc/any/staging.yml")
	cfg, err = Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Push.IsEnabled() {
		t.Error("explicit true should read as enabled")
	}
	if cfg.Push.Active() {
		t.Error("enabled without peer info must not be active")
	}
}

func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANY_DATA_DIR", "ANY_MODE", "ANY_ACCOUNT", "ANY_LISTEN_ADDR", "ANY_WALLET_PATH", "ANY_LOG_LEVEL",
		"ANY_WALLET_PASSKEY", "XDG_CONFIG_HOME", "ANY_NETWORK_NODECONF_PATH",
		"ANY_PUSH_ENABLED", "ANY_PUSH_PEER_ID", "ANY_PUSH_ADDRS",
		"ANY_INDEX_ENABLED", "ANY_INDEX_EMBEDDER",
		"ANY_INDEX_OLLAMA_URL", "ANY_INDEX_OLLAMA_MODEL",
		"ANY_INDEX_OPENAI_BASE_URL", "ANY_INDEX_OPENAI_MODEL",
		"ANY_INDEX_OPENAI_API_KEY", "ANY_INDEX_VECTOR_DIM",
		"ANY_INDEX_LOCAL_MODEL_PATH", "ANY_INDEX_LOCAL_MODEL_URL",
		"ANY_INDEX_LOCAL_MODEL_SHA256", "ANY_INDEX_LOCAL_LIB_DIR",
		"ANY_INDEX_LOCAL_CONTEXT_SIZE", "ANY_INDEX_LOCAL_DIM",
		"ANY_INDEX_LOCAL_QUERY_PREFIX",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// Point HOME at a tmp dir so ConfigSearchPaths doesn't find a real file.
	t.Setenv("HOME", t.TempDir())
}
