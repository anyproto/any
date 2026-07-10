// Package e2e exercises the compiled `any` binary against the staging
// network: it builds cmd/any, runs `any run` against a temp data dir,
// and drives every shipped endpoint (real and 501) over real HTTP.
//
// The test is gated on the same staging.yml fixture the SDK uses for
// its own integration tests; it skips silently when the file is absent.
// Set ANY_E2E_KEEP=1 to keep the temp data dir on success for poking.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/api"
)

// stagingFixture is the staging nodeconf the e2e tests boot against.
// It lives at the repo root and is gitignored (not checked in) — copy
// your own staging.yml there to run these tests. Path is relative to
// this package dir (`internal/e2e`), which is `go test`'s CWD.
const stagingFixture = "../../staging.yml"

// absStagingPath returns the absolute path to the staging fixture so a
// spawned `any` binary (which has a different CWD than this test) can
// locate it. The default DefaultNodeconfPath is CWD-relative; surfacing
// it explicitly via a config.yaml is what an out-of-tree caller would
// also have to do. Skips the test (rather than failing) when the
// fixture is absent, since it isn't checked in.
func absStagingPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(stagingFixture)
	if err != nil {
		t.Fatalf("resolve staging path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("staging fixture %s not found — copy a staging.yml to the repo root to run e2e tests", abs)
	}
	return abs
}

// TestE2E_FullFlow boots the binary, drives every implemented endpoint
// plus a representative 501, and shuts down via POST /v1/shutdown.
func TestE2E_FullFlow(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	t.Logf("data dir: %s", dataDir)
	t.Logf("listen:   %s", addr)

	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)

	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	t.Run("GET /v1/health", func(t *testing.T) {
		var h map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/health", "", http.StatusOK, &h)
		if h["status"] != "ok" {
			t.Errorf("status = %v, want ok", h["status"])
		}
		if h["account"] == nil || h["account"] == "" {
			t.Errorf("account empty: %+v", h)
		}
	})

	t.Run("GET /v1/account", func(t *testing.T) {
		var acc map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/account", "", http.StatusOK, &acc)
		if acc["id"] == nil || acc["id"] == "" {
			t.Errorf("id empty: %+v", acc)
		}
		// Metadata is reserved (omitempty) until the SDK wires it.
		if _, ok := acc["metadata"]; ok {
			t.Errorf("metadata should be omitted, got %+v", acc)
		}
	})

	t.Run("PUT /v1/account/metadata", func(t *testing.T) {
		body := `{"name":"E2E","description":"smoke","iconCid":"bafyfake"}`
		mustStatus(t, http.MethodPut, base+"/v1/account/metadata", body, http.StatusNoContent)

		// All fields empty → 400 request.missing_field.
		var env map[string]any
		mustJSON(t, http.MethodPut, base+"/v1/account/metadata", `{}`, http.StatusBadRequest, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != "request.missing_field" {
			t.Errorf("empty body code = %v, want request.missing_field", errObj["code"])
		}
	})

	t.Run("GET /v1/spaces empty", func(t *testing.T) {
		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 0 {
			t.Errorf("expected empty list, got %+v", spaces)
		}
	})

	var (
		spaceID        string
		spaceCreatedAt time.Time
	)
	t.Run("POST /v1/spaces creates", func(t *testing.T) {
		var created map[string]any
		body := `{"name":"E2E","description":"e2e-smoke"}`
		mustJSON(t, http.MethodPost, base+"/v1/spaces", body, http.StatusCreated, &created)
		id, _ := created["id"].(string)
		if id == "" {
			t.Fatalf("no id in %+v", created)
		}
		spaceID = id
		spaceCreatedAt = time.Now()
		if got := created["name"]; got != "E2E" {
			t.Errorf("name = %v, want E2E", got)
		}
		if got := created["status"]; got != "active" {
			t.Errorf("status = %v, want active", got)
		}
	})

	if spaceID == "" {
		t.Fatal("no spaceID — skipping the rest")
	}

	t.Run("types + properties demo flow", func(t *testing.T) {
		// Mirrors any-sync-sdk/sdk_test.go: TestSDK_TypesAndProperties
		// against the real binary over loopback HTTP.
		var typeResp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types",
			`{"name":"Movie","description":"A film","xKey":"movie"}`,
			http.StatusCreated, &typeResp)
		typeID, _ := typeResp["typeId"].(string)
		if typeID == "" {
			t.Fatalf("typeId empty: %+v", typeResp)
		}

		var propResp map[string]any
		mustJSON(t, http.MethodPost,
			base+"/v1/spaces/"+spaceID+"/types/"+typeID+"/properties",
			`{"name":"Title","kind":"string","xKey":"title"}`,
			http.StatusCreated, &propResp)
		propID, _ := propResp["propId"].(string)
		if propID == "" {
			t.Fatalf("propId empty: %+v", propResp)
		}

		var objResp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects",
			fmt.Sprintf(`{"types":[%q]}`, typeID),
			http.StatusCreated, &objResp)
		objectID, _ := objResp["objectId"].(string)
		if objectID == "" {
			t.Fatalf("objectId empty: %+v", objResp)
		}

		var modify map[string]any
		mustJSON(t, http.MethodPost,
			base+"/v1/spaces/"+spaceID+"/properties/"+objectID+"/set/"+typeID,
			fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, propID),
			http.StatusOK, &modify)
		if modify["versionId"] == "" || modify["changeId"] == "" {
			t.Errorf("modify result incomplete: %+v", modify)
		}

		var got map[string]any
		mustJSON(t, http.MethodGet,
			base+"/v1/spaces/"+spaceID+"/properties/"+objectID, "",
			http.StatusOK, &got)
		record, _ := got["record"].(map[string]any)
		typeNode, _ := record[typeID].(map[string]any)
		if typeNode[propID] != "Casablanca" {
			t.Errorf("record[%s][%s] = %v, want Casablanca; full=%+v",
				typeID, propID, typeNode[propID], record)
		}

		// Regression: nav is registered with the SDK (property-only type),
		// so Types().List already surfaces it. The handler must NOT inject
		// it a second time — clients reported getting "nav" twice. Assert
		// generally that no type id appears more than once, and that nav
		// (plus the just-created type) is present.
		var typesList map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID+"/types", "",
			http.StatusOK, &typesList)
		types, _ := typesList["types"].([]any)
		counts := map[string]int{}
		for _, ti := range types {
			if m, ok := ti.(map[string]any); ok {
				if id, _ := m["id"].(string); id != "" {
					counts[id]++
				}
			}
		}
		for id, n := range counts {
			if n != 1 {
				t.Errorf("type %q appears %d times in /types, want exactly 1; full=%+v",
					id, n, types)
			}
		}
		if counts["nav"] == 0 {
			t.Errorf("nav type missing from /types; full=%+v", types)
		}
		if counts[typeID] == 0 {
			t.Errorf("created type %q missing from /types; full=%+v", typeID, types)
		}
	})

	t.Run("GET /v1/spaces/:id", func(t *testing.T) {
		var got map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID, "", http.StatusOK, &got)
		if got["id"] != spaceID {
			t.Errorf("id mismatch: got %v want %v", got["id"], spaceID)
		}
		// spaceIndexObjectId is the deterministic derived id of the
		// in-space spaceIndex object; single-space responses always
		// carry it.
		if id, _ := got["spaceIndexObjectId"].(string); id == "" {
			t.Errorf("spaceIndexObjectId missing: %+v", got)
		}
		// createdAt is stamped on the tech-space row at create time
		// (added-to-account semantics) — must be a real, recent RFC3339
		// time, not the zero value pre-stamp rows report.
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(got["createdAt"]))
		if err != nil {
			t.Fatalf("createdAt not RFC3339: %v (%v)", got["createdAt"], err)
		}
		if time.Since(ts) > time.Hour {
			t.Errorf("createdAt = %v, want recent (zero means the stamp is missing)", ts)
		}
	})

	t.Run("PATCH /v1/spaces/:id renames", func(t *testing.T) {
		// Empty body → 400 request.missing_field.
		mustStatus(t, http.MethodPatch, base+"/v1/spaces/"+spaceID, `{}`, http.StatusBadRequest)

		// Patch name only — description/iconCid stay.
		mustStatus(t, http.MethodPatch, base+"/v1/spaces/"+spaceID,
			`{"name":"E2E-renamed"}`, http.StatusNoContent)

		// The mirror back into the tech-space row runs async — poll
		// for up to 5s rather than asserting on the immediate read.
		var got map[string]any
		deadline := time.Now().Add(5 * time.Second)
		for {
			mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID, "", http.StatusOK, &got)
			if got["name"] == "E2E-renamed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("name never converged to E2E-renamed; last=%+v", got)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if got["description"] != "e2e-smoke" {
			t.Errorf("description clobbered: %+v", got)
		}
	})

	t.Run("GET /v1/spaces lists newly-created", func(t *testing.T) {
		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 1 {
			t.Fatalf("want 1 space, got %d: %+v", len(spaces), spaces)
		}
	})

	t.Run("GET /v1/datasets system schemas", func(t *testing.T) {
		var resp map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/datasets", "", http.StatusOK, &resp)
		ds, _ := resp["datasets"].([]any)
		if !datasetPresent(ds, "spaces") {
			t.Fatalf("system datasets missing `spaces`: %+v", ds)
		}
		// The `spaces` schema must be a JSON Schema object with x-scope
		// annotations on its fields (discovery contract).
		sch := datasetSchemaByName(ds, "spaces")
		if sch["type"] != "object" {
			t.Errorf("spaces schema not a JSON Schema object: %+v", sch)
		}
	})

	t.Run("GET /v1/spaces/:id/datasets", func(t *testing.T) {
		var resp map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID+"/datasets", "", http.StatusOK, &resp)
		ds, _ := resp["datasets"].([]any)
		// The built-in handler datasets must surface with declared
		// schemas (the SDK schema feature wired through any's handlers).
		for _, name := range []string{"objects", "chat_messages", "editor_blocks"} {
			if !datasetPresent(ds, name) {
				t.Errorf("space datasets missing %q: %+v", name, ds)
			}
		}
		// chat_messages declares `creator` as a derived field — assert
		// the x-scope annotation rode through to the wire.
		chat := datasetSchemaByName(ds, "chat_messages")
		props, _ := chat["properties"].(map[string]any)
		creator, _ := props["creator"].(map[string]any)
		if creator["x-scope"] != "derived" {
			t.Errorf("chat_messages.creator x-scope = %v, want derived: %+v", creator["x-scope"], creator)
		}
	})

	t.Run("POST /v1/spaces/query windowed snapshot", func(t *testing.T) {
		var resp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/query", `{"includeTotal":true}`, http.StatusOK, &resp)
		records, _ := resp["records"].([]any)
		if len(records) != 1 {
			t.Fatalf("want 1 space record, got %d: %+v", len(records), records)
		}
		rec, _ := records[0].(map[string]any)
		if rec["id"] != spaceID {
			t.Errorf("record id = %v, want %v", rec["id"], spaceID)
		}
		// Raw rows carry the handler-derived createdAt as unix seconds.
		if ts, _ := rec["createdAt"].(float64); ts <= 0 {
			t.Errorf("createdAt = %v, want positive unix seconds: %+v", rec["createdAt"], rec)
		}
	})

	t.Run("POST /v1/spaces/query/subscribe streams live changes", func(t *testing.T) {
		// Open the windowed space-list SSE stream and assert the initial
		// ready → snapshot frames, then trigger an `updated` change via a
		// PATCH rename and assert it streams through. PATCH (not a second
		// create) so the space count the DELETE subtest asserts stays at 1.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		frames, errc := openSSE(ctx, t, http.MethodPost, base+"/v1/spaces/query/subscribe", `{}`)

		if f := waitSSE(t, frames, errc, "ready", 15*time.Second); f.Event != "ready" {
			t.Fatalf("first frame = %q, want ready", f.Event)
		}
		snap := waitSSE(t, frames, errc, "snapshot", 15*time.Second)
		var snapData struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal(snap.Data, &snapData); err != nil {
			t.Fatalf("snapshot decode: %v", err)
		}
		if len(snapData.Records) != 1 || snapData.Records[0]["id"] != spaceID {
			t.Fatalf("snapshot records = %+v, want [%s]", snapData.Records, spaceID)
		}

		mustStatus(t, http.MethodPatch, base+"/v1/spaces/"+spaceID, `{"name":"E2E-sub"}`, http.StatusNoContent)

		// The rename arrives as a `changes` frame carrying an `updated`
		// record for our space. Tolerate intervening frames.
		deadline := time.Now().Add(20 * time.Second)
		for {
			f := waitSSE(t, frames, errc, "changes", time.Until(deadline))
			var batch []struct {
				Updated []map[string]any `json:"updated"`
			}
			if err := json.Unmarshal(f.Data, &batch); err != nil {
				t.Fatalf("changes decode: %v", err)
			}
			found := false
			for _, ev := range batch {
				for _, u := range ev.Updated {
					if u["id"] == spaceID {
						found = true
					}
				}
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("never saw an `updated` change for %s", spaceID)
			}
		}
	})

	t.Run("DELETE /v1/spaces/:id soft-deletes", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, base+"/v1/spaces/"+spaceID, "", http.StatusNoContent)

		// Default list hides non-active rows (temporary workaround in
		// listSpaces) — ask for all statuses to see the deleted row.
		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces?status=all", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 1 {
			t.Fatalf("want 1 (deleted) row in list, got %d", len(spaces))
		}
		entry, _ := spaces[0].(map[string]any)
		if entry["status"] != "deleted" {
			t.Errorf("status = %v, want deleted", entry["status"])
		}
	})

	t.Run("POST /v1/spaces/query sorts by -createdAt", func(t *testing.T) {
		// Second space (the soft-deleted first one stays in the index),
		// so the newest-first sort has two rows to order. createdAt has
		// one-second resolution — top up to >1s since the first create
		// so the two stamps can't tie (in practice the subtests in
		// between already took far longer and this never sleeps).
		if d := 1100*time.Millisecond - time.Since(spaceCreatedAt); d > 0 {
			time.Sleep(d)
		}
		var created map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces", `{"name":"E2E-second"}`, http.StatusCreated, &created)
		secondID, _ := created["id"].(string)
		if secondID == "" {
			t.Fatalf("no id in %+v", created)
		}

		var resp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/query", `{"sort":["-createdAt"]}`, http.StatusOK, &resp)
		records, _ := resp["records"].([]any)
		if len(records) != 2 {
			t.Fatalf("want 2 space records, got %d: %+v", len(records), records)
		}
		first, _ := records[0].(map[string]any)
		second, _ := records[1].(map[string]any)
		if first["id"] != secondID || second["id"] != spaceID {
			t.Errorf("-createdAt order = [%v %v], want [%v %v]",
				first["id"], second["id"], secondID, spaceID)
		}
	})

	t.Run("501 routes", func(t *testing.T) {
		cases := []struct{ method, path string }{
			{http.MethodPost, "/v1/spaces/derive"},
			{http.MethodDelete, "/v1/spaces/" + spaceID + "/types/t1"},
			{http.MethodDelete, "/v1/spaces/" + spaceID + "/types/t1/properties/p1"},
			{http.MethodPatch, "/v1/spaces/" + spaceID + "/types/t1/properties/p1"},
			{http.MethodGet, "/v1/spaces/" + spaceID + "/sync-status/peers"},
		}
		for _, tc := range cases {
			var env map[string]any
			body := "{}"
			if tc.method == http.MethodGet {
				body = ""
			}
			mustJSON(t, tc.method, base+tc.path, body, http.StatusNotImplemented, &env)
			errObj, _ := env["error"].(map[string]any)
			if errObj["code"] != "sdk.not_implemented" {
				t.Errorf("%s %s: code = %v, want sdk.not_implemented", tc.method, tc.path, errObj["code"])
			}
		}
	})

	t.Run("POST /v1/shutdown", func(t *testing.T) {
		mustStatus(t, http.MethodPost, base+"/v1/shutdown", "", http.StatusNoContent)
	})

	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit cleanly: %v\n%s", err, srv.output())
	}
	if srv.exitErr != nil && !errors.Is(srv.exitErr, &exec.ExitError{}) {
		t.Logf("server exit: %v", srv.exitErr)
	}

	if os.Getenv("ANY_E2E_KEEP") == "" {
		_ = os.RemoveAll(dataDir)
	}
}

// TestE2E_AnyStatus exercises the CLI subcommand path against the
// running server. Confirms the CLI talks JSON to the same endpoint a
// raw HTTP client does.
func TestE2E_AnyStatus(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	out, err := exec.Command(bin, "--addr", addr, "status").CombinedOutput()
	if err != nil {
		t.Fatalf("any status: %v\n%s", err, out)
	}
	var h map[string]any
	if err := json.Unmarshal(out, &h); err != nil {
		t.Fatalf("decode any-status output: %v\nraw:\n%s", err, out)
	}
	if h["status"] != "ok" {
		t.Errorf("status field = %v", h["status"])
	}

	// And `any stop` triggers shutdown.
	stopOut, err := exec.Command(bin, "--addr", addr, "stop").CombinedOutput()
	if err != nil {
		t.Fatalf("any stop: %v\n%s", err, stopOut)
	}
	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit after `any stop`: %v\n%s", err, srv.output())
	}
}

// TestE2E_InitMnemonicIndex is the F1 round-trip: `init --mnemonic
// --index N` must create the wallet at index N (so its dir name and
// reported id are the index-N account), and a subsequent `run` must
// then boot authorized as that same account — the account-id
// verification in bootEngine fails loudly if the wallet was seeded at
// the wrong index.
func TestE2E_InitMnemonicIndex(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()

	m, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	wantID, err := auth.AccountId(m, 2)
	if err != nil {
		t.Fatal(err)
	}
	if id0, _ := auth.AccountId(m, 0); id0 == wantID {
		t.Fatal("index 0 and 2 collided — pick a real index for the test")
	}

	out, err := exec.Command(bin, "init", "--data-dir", dataDir, "--mnemonic", m, "--index", "2").Output()
	if err != nil {
		t.Fatalf("any init --mnemonic --index 2: %v", err)
	}
	var initResp struct {
		AccountId string `json:"accountId"`
		Created   bool   `json:"created"`
	}
	if err := json.Unmarshal(out, &initResp); err != nil {
		t.Fatalf("decode init output: %v\nraw:\n%s", err, out)
	}
	if initResp.AccountId != wantID || !initResp.Created {
		t.Fatalf("init reply %+v, want accountId %s created true", initResp, wantID)
	}

	// run auto-selects the sole account and boots authorized as it —
	// proves the on-disk wallet really derives the index-2 id.
	addr := freeLoopbackAddr(t)
	srv := startServerInit(t, bin, addr, dataDir, false)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	var acct map[string]any
	mustJSON(t, http.MethodGet, "http://"+addr+"/v1/account", "", http.StatusOK, &acct)
	if acct["id"] != wantID {
		t.Fatalf("booted account %v, want %s", acct["id"], wantID)
	}
}

// TestE2E_AuthFlow exercises the deferred-boot onboarding path end to
// end at the binary level: an account-less `run` starts unauthorized,
// `any auth login` (POST /v1/auth) generates the account and boots the
// SDK in place, and a restart auto-selects the created identity.
func TestE2E_AuthFlow(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServerInit(t, bin, addr, dataDir, false)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)
	base := "http://" + addr

	// Unauthorized: SDK routes are guarded, auth status is open.
	resp, raw := doRequest(t, http.MethodGet, base+"/v1/spaces", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-auth GET /v1/spaces: want 401, got %d %s", resp.StatusCode, raw)
	}
	var st api.AuthStatusResponse
	mustJSON(t, http.MethodGet, base+"/v1/auth", "", http.StatusOK, &st)
	if st.Authorized || len(st.Accounts) != 0 {
		t.Fatalf("pre-auth status: %+v", st)
	}

	// `any auth login` with no flags generates an account and boots.
	out, err := exec.Command(bin, "--addr", addr, "auth", "login").Output()
	if err != nil {
		t.Fatalf("any auth login: %v\n%s", err, out)
	}
	var login api.AuthResponse
	if err := json.Unmarshal(out, &login); err != nil {
		t.Fatalf("decode auth login output: %v\nraw:\n%s", err, out)
	}
	if login.AccountId == "" || !login.Created {
		t.Fatalf("auth login reply: %+v", login)
	}

	var acct map[string]any
	mustJSON(t, http.MethodGet, base+"/v1/account", "", http.StatusOK, &acct)
	if acct["id"] != login.AccountId {
		t.Fatalf("account id %v != login reply %s", acct["id"], login.AccountId)
	}

	// Double-auth → 409.
	resp, _ = doRequest(t, http.MethodPost, base+"/v1/auth", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second POST /v1/auth: want 409, got %d", resp.StatusCode)
	}

	// Restart: the created identity is the sole account and is
	// auto-selected — server boots authorized.
	stopOut, err := exec.Command(bin, "--addr", addr, "stop").CombinedOutput()
	if err != nil {
		t.Fatalf("any stop: %v\n%s", err, stopOut)
	}
	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit: %v\n%s", err, srv.output())
	}
	srv2 := startServerInit(t, bin, addr, dataDir, false)
	defer srv2.stop(t)
	waitForReady(t, addr, 30*time.Second)
	mustJSON(t, http.MethodGet, base+"/v1/auth", "", http.StatusOK, &st)
	if !st.Authorized || st.AccountId != login.AccountId {
		t.Fatalf("post-restart status: %+v", st)
	}
}

// --- helpers --------------------------------------------------------------

type runningServer struct {
	cmd     *exec.Cmd
	out     *bytes.Buffer
	done    chan struct{}
	exitErr error
}

func startServer(t *testing.T, bin, addr, dataDir string) *runningServer {
	return startServerInit(t, bin, addr, dataDir, true)
}

// startServerInit is startServer with the account-init step optional:
// withInit=false boots an UNAUTHORIZED server (fresh root, no wallet)
// for tests exercising the POST /v1/auth onboarding path.
func startServerInit(t *testing.T, bin, addr, dataDir string, withInit bool) *runningServer {
	return startServerConf(t, bin, addr, dataDir, withInit, absStagingPath(t))
}

// startServerConf is the fully-parameterised boot: nodeconfAbs picks
// which network the peer joins (the file-latency benchmark points it
// at a local-infra nodeconf with fileV2 nodes instead of staging).
func startServerConf(t *testing.T, bin, addr, dataDir string, withInit bool, nodeconfAbs string) *runningServer {
	t.Helper()
	// Drop a config.yaml that pins network.nodeconfPath to an absolute
	// path. The binary's compile-time default is CWD-relative, which
	// won't resolve when the test exec's it from a different working
	// directory.
	cfgPath := filepath.Join(dataDir, "config.yaml")
	// embedder: none keeps e2e hermetic — the default "auto" would call the
	// online embedding API (DeepInfra) on every boot. e2e doesn't exercise
	// vector search, so FTS-only is fine.
	cfgBody := fmt.Sprintf("network:\n  nodeconfPath: %s\nindex:\n  embedder: none\n", nodeconfAbs)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// `run` no longer auto-creates a wallet (it would start unauthorized
	// and wait for POST /v1/auth) — init the account first. A no-op when
	// the test pre-seeded a root wallet.key.
	if withInit {
		initCmd := exec.Command(bin, "init", "--config", cfgPath, "--data-dir", dataDir)
		initCmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
		if out, err := initCmd.CombinedOutput(); err != nil {
			t.Fatalf("any init: %v\n%s", err, out)
		}
	}

	return execServer(t, bin, cfgPath, dataDir, addr)
}

// execServer starts `any run` against an existing config.yaml. Split
// from startServerInit so tests that need a NON-staging config (e.g.
// the offline p2p cold-restore test) can write their own and still
// share the process plumbing.
func execServer(t *testing.T, bin, cfgPath, dataDir, addr string) *runningServer {
	t.Helper()
	cmd := exec.Command(bin, "run", "--config", cfgPath, "--data-dir", dataDir, "--addr", addr)
	// Inherit env but force ANY_DATA_DIR so the binary never picks up the
	// developer's home dir.
	cmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	rs := &runningServer{cmd: cmd, out: &buf, done: make(chan struct{})}
	go func() {
		rs.exitErr = cmd.Wait()
		close(rs.done)
	}()
	return rs
}

func (s *runningServer) stop(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
		// Already exited (crash, external kill, SIGQUIT dump) — the
		// buffered output is the most interesting artifact there is.
		if t.Failed() || os.Getenv("ANY_E2E_DUMP") != "" {
			t.Logf("server output (exited early):\n%s", s.out.String())
		}
		return
	default:
	}
	stopStart := time.Now()
	if runtime.GOOS == "windows" {
		_ = s.cmd.Process.Kill()
	} else {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}
	killed := false
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
		killed = true
	}
	if os.Getenv("ANY_E2E_DUMP") != "" {
		t.Logf("stop took %s (killed=%v)", time.Since(stopStart), killed)
	}
	if t.Failed() || os.Getenv("ANY_E2E_DUMP") != "" {
		t.Logf("server output:\n%s", s.out.String())
	}
}

func (s *runningServer) waitExit(d time.Duration) error {
	select {
	case <-s.done:
		return nil
	case <-time.After(d):
		return fmt.Errorf("timed out waiting %s for exit", d)
	}
}

func (s *runningServer) output() string { return s.out.String() }

func waitForReady(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			// also wait for /v1/health to actually 200 — the listener may
			// be up before the SDK finishes booting.
			r, err := http.Get("http://" + addr + "/v1/health")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", addr)
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "any")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "github.com/anyproto/any/cmd/any")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func mustJSON(t *testing.T, method, url, body string, wantStatus int, out any) {
	t.Helper()
	resp, raw := doRequest(t, method, url, body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode: %v body=%s", method, url, err, raw)
		}
	}
}

func mustStatus(t *testing.T, method, url, body string, wantStatus int) {
	t.Helper()
	resp, raw := doRequest(t, method, url, body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, raw)
	}
}

// sseFrame is one parsed Server-Sent Events frame (event + joined data).
type sseFrame struct {
	Event string
	Data  []byte
}

// openSSE issues a streaming request and parses the SSE response into
// frames delivered on the returned channel. A terminal error (including
// io.EOF when the stream closes) arrives on errc. The caller cancels ctx
// to tear the stream down. Uses a no-timeout client — the request
// timeout on the shared client is useless for long-lived streams.
func openSSE(ctx context.Context, t *testing.T, method, url, body string) (<-chan sseFrame, <-chan error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("open SSE %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("open SSE %s: status=%d body=%s", url, resp.StatusCode, raw)
	}
	frames := make(chan sseFrame, 64)
	errc := make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		defer close(frames)
		br := bufio.NewReader(resp.Body)
		var event string
		var data bytes.Buffer
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				ln := bytes.TrimRight(line, "\r\n")
				switch {
				case len(ln) == 0: // frame terminator
					if event != "" || data.Len() > 0 {
						frames <- sseFrame{Event: event, Data: append([]byte(nil), bytes.TrimSuffix(data.Bytes(), []byte("\n"))...)}
					}
					event = ""
					data.Reset()
				case ln[0] == ':': // comment / keepalive
				case bytes.HasPrefix(ln, []byte("event:")):
					event = string(bytes.TrimSpace(ln[len("event:"):]))
				case bytes.HasPrefix(ln, []byte("data:")):
					data.Write(bytes.TrimPrefix(ln[len("data:"):], []byte(" ")))
					data.WriteByte('\n')
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	return frames, errc
}

// waitSSE blocks until a frame with the wanted event name arrives (within
// timeout), skipping other frames (e.g. keepalives). Fails the test on
// timeout or stream error.
func waitSSE(t *testing.T, frames <-chan sseFrame, errc <-chan error, want string, timeout time.Duration) sseFrame {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE stream closed before %q frame", want)
			}
			if f.Event == want {
				return f
			}
		case err := <-errc:
			t.Fatalf("SSE stream error before %q frame: %v", want, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %q frame", want)
		}
	}
}

// datasetPresent reports whether a {name, schema} entry with the given
// name is in the datasets array returned by GET /v1/[spaces/:id/]datasets.
func datasetPresent(datasets []any, name string) bool {
	for _, d := range datasets {
		if m, ok := d.(map[string]any); ok && m["name"] == name {
			return true
		}
	}
	return false
}

// datasetSchemaByName returns the parsed JSON Schema object for the named
// dataset, or nil. The schema rides as an embedded object on the wire.
func datasetSchemaByName(datasets []any, name string) map[string]any {
	for _, d := range datasets {
		m, ok := d.(map[string]any)
		if !ok || m["name"] != name {
			continue
		}
		sch, _ := m["schema"].(map[string]any)
		return sch
	}
	return nil
}

// e2eRequestTimeout bounds every non-streaming e2e request. Without it a
// single stalled SDK call (e.g. POST /v1/spaces blocking on a degraded
// staging coordinator) hangs forever, consuming the whole package's 10m
// `go test` budget and panicking — which aborts every other e2e test and
// dumps goroutine stacks instead of naming the culprit. With it, a stuck
// call fails just that test, fast, with "context deadline exceeded", and
// the rest of the suite still runs. Streaming (SSE) requests use their
// own no-timeout client in openSSE. Override via ANY_E2E_HTTP_TIMEOUT
// (Go duration, e.g. "120s") for slow environments.
var e2eClient = &http.Client{Timeout: e2eHTTPTimeout()}

func e2eHTTPTimeout() time.Duration {
	if v := os.Getenv("ANY_E2E_HTTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 90 * time.Second
}

func doRequest(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}
