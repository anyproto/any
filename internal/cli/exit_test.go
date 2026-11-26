package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/client"
)

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"user", errors.New("bad flag"), 1},
		{"4xx", &client.ServerError{Status: 404}, 1},
		{"5xx", &client.ServerError{Status: 500}, 2},
		{"cut stream", fmt.Errorf("export: %w", &client.StreamError{Err: io.ErrUnexpectedEOF}), 2},
		{"unreachable", &client.TransportError{Err: errors.New("refused")}, 3},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("%s: exitCode = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Output that cannot be written fails the command: a script reading it
// sees exit 1, not a success with nothing on stdout.
func TestPrintJSON_FailedWrite(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close() // the reader went away
	defer w.Close()
	stdout := os.Stdout
	os.Stdout = w
	err = printJSON(map[string]int{"n": 1})
	os.Stdout = stdout
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("printJSON to a closed pipe = %v, want an error that exits 1", err)
	}
}

// `any status` and `any init` fail too when their result cannot be
// written.
func TestStatusAndInit_FailedWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	addr := flags.Addr
	flags.Addr = strings.TrimPrefix(srv.URL, "http://")
	defer func() { flags.Addr = addr }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close() // the reader went away
	defer w.Close()
	status := newStatusCmd()
	status.SetContext(context.Background())
	stdout := os.Stdout
	os.Stdout = w
	err = status.RunE(status, nil)
	os.Stdout = stdout
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("status to a closed pipe = %v, want an error that exits 1", err)
	}

	ic := newInitCmd()
	ic.SetOut(w)
	if err := printInitResult(ic, "Aone", true); err == nil || exitCode(err) != 1 {
		t.Fatalf("init's result to a closed pipe = %v, want an error that exits 1", err)
	}
}

// failingWriter fails every write, as a full disk does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A fresh `any init` whose recovery phrase cannot be written exits 1 and
// says the wallet it created is there: the phrase is shown only once.
func TestInit_RecoveryPhraseFailedWrite(t *testing.T) {
	phrase, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	ic := newInitCmd()
	ic.SetContext(context.Background())
	ic.SetErr(failingWriter{})
	var out bytes.Buffer
	ic.SetOut(&out)
	wallet := filepath.Join(t.TempDir(), "wallet.key")
	err = initWallet(ic, wallet, "", phrase, auth.DefaultAccountIndex, true)
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "wallet was created") {
		t.Fatalf("init with its phrase unwritten = %v, want an error that exits 1 and names the created wallet", err)
	}
	if _, err := os.Stat(wallet); err != nil {
		t.Fatalf("the wallet: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a result printed after the phrase was lost: %s", out.String())
	}
}
