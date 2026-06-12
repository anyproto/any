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
func TestLoad_IndexDefaults(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Index.Enabled {
		t.Error("index.enabled should default true")
	}
	if cfg.Index.Embedder != "local" {
		t.Errorf("index.embedder should default to local, got %q", cfg.Index.Embedder)
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

	// Env wins over file.
	t.Setenv("ANY_INDEX_LOCAL_MODEL_PATH", "/models/other.gguf")
	t.Setenv("ANY_INDEX_LOCAL_LIB_DIR", "/opt/llamacpp")
	t.Setenv("ANY_INDEX_LOCAL_CONTEXT_SIZE", "1024")
	t.Setenv("ANY_INDEX_LOCAL_DIM", "512")
	cfg, err = Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Index.Local.ModelPath != "/models/other.gguf" || cfg.Index.Local.LibDir != "/opt/llamacpp" ||
		cfg.Index.Local.ContextSize != 1024 || cfg.Index.Local.Dim != 512 {
		t.Errorf("env values not applied: %+v", cfg.Index.Local)
	}
}

func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANY_DATA_DIR", "ANY_LISTEN_ADDR", "ANY_WALLET_PATH", "ANY_LOG_LEVEL",
		"ANY_WALLET_PASSKEY", "XDG_CONFIG_HOME",
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
