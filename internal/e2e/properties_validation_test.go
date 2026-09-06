// Property-validation e2e: drives the SDK's write-time schema validation
// end-to-end through the HTTP server. The SDK now resolves a registered
// type's property manifest and rejects writes whose value kind doesn't
// match the declared kind, whose property is undeclared, or whose type
// the object doesn't implement. This test asserts the happy path lands
// and every rejection surfaces as a 400 with the documented error code
// (docs/06-errors.md) — not a 500.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestE2E_PropertyValidation(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	t.Logf("data dir: %s", dataDir)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	// Scaffolding: a space, a user type with a string + number property,
	// a second type the object will NOT implement, and one object.
	spaceID := createSpace(t, base, "PropVal", "property validation e2e")
	movieType := createType(t, base, spaceID, "Movie")
	titleProp := addProperty(t, base, spaceID, movieType, "Title", "string")
	yearProp := addProperty(t, base, spaceID, movieType, "Year", "number")

	bookType := createType(t, base, spaceID, "Book")
	bookTitle := addProperty(t, base, spaceID, bookType, "Title", "string")

	objectID := createObject(t, base, spaceID, movieType)

	setBase := base + "/v1/spaces/" + spaceID + "/properties/" + objectID + "/set/"

	t.Run("valid string write lands", func(t *testing.T) {
		var res map[string]any
		mustJSON(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, titleProp),
			http.StatusOK, &res)
		if res["versionId"] == "" || res["changeId"] == "" {
			t.Errorf("modify result incomplete: %+v", res)
		}
	})

	t.Run("valid number write lands and reads back", func(t *testing.T) {
		var res map[string]any
		mustJSON(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:1942}}`, yearProp),
			http.StatusOK, &res)
		assertProp(t, base, spaceID, objectID, movieType, titleProp, "Casablanca")
		assertProp(t, base, spaceID, objectID, movieType, yearProp, float64(1942))
	})

	t.Run("kind mismatch: number into string prop", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:1942}}`, titleProp), http.StatusBadRequest)
		if code != "property.kind_mismatch" {
			t.Errorf("code = %q, want property.kind_mismatch", code)
		}
	})

	t.Run("kind mismatch: string into number prop", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:"nineteen"}}`, yearProp), http.StatusBadRequest)
		if code != "property.kind_mismatch" {
			t.Errorf("code = %q, want property.kind_mismatch", code)
		}
	})

	t.Run("kind mismatch: bool into number prop", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:true}}`, yearProp), http.StatusBadRequest)
		if code != "property.kind_mismatch" {
			t.Errorf("code = %q, want property.kind_mismatch", code)
		}
	})

	t.Run("unknown property on known type", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost, setBase+movieType,
			`{"patch":{"not_a_real_prop":"x"}}`, http.StatusBadRequest)
		if code != "property.not_found" {
			t.Errorf("code = %q, want property.not_found", code)
		}
	})

	t.Run("type not implemented by object", func(t *testing.T) {
		// bookType/bookTitle are well-formed, but the object's any.types
		// is [Movie] — it never implemented Book, so the write is
		// rejected before the (valid) property is even consulted.
		code := mustErrorCode(t, http.MethodPost, setBase+bookType,
			fmt.Sprintf(`{"patch":{%q:"x"}}`, bookTitle), http.StatusBadRequest)
		if code != "dataset.validation" {
			t.Errorf("code = %q, want dataset.validation", code)
		}
	})

	t.Run("mixed valid+invalid rejects the whole write atomically", func(t *testing.T) {
		// title is a valid string, year gets a string (wrong kind). The
		// SDK rejects the entire write — title must stay Casablanca.
		code := mustErrorCode(t, http.MethodPost, setBase+movieType,
			fmt.Sprintf(`{"patch":{%q:"Vertigo",%q:"bad"}}`, titleProp, yearProp),
			http.StatusBadRequest)
		if code != "property.kind_mismatch" {
			t.Errorf("code = %q, want property.kind_mismatch", code)
		}
		assertProp(t, base, spaceID, objectID, movieType, titleProp, "Casablanca")
	})

	// A standalone server refuses HTTP shutdown; `any stop` signals it.
	mustStatus(t, http.MethodPost, base+"/v1/shutdown", "", http.StatusForbidden)
	if out, err := anyStop(t, bin, dataDir); err != nil {
		t.Fatalf("any stop: %v\n%s", err, out)
	}
	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit cleanly: %v\n%s", err, srv.output())
	}
}

// --- helpers --------------------------------------------------------------

func createSpace(t *testing.T, base, name, desc string) string {
	t.Helper()
	var created map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		fmt.Sprintf(`{"name":%q,"description":%q}`, name, desc),
		http.StatusCreated, &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("createSpace: no id in %+v", created)
	}
	return id
}

func createType(t *testing.T, base, spaceID, name string) string {
	t.Helper()
	var resp map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types",
		fmt.Sprintf(`{"name":%q,"xKey":%q}`, name, slugXKey(name)), http.StatusCreated, &resp)
	id, _ := resp["typeId"].(string)
	if id == "" {
		t.Fatalf("createType %q: typeId empty: %+v", name, resp)
	}
	return id
}

// slugXKey lowercases a type name into a snake_case xKey — enough for
// tests to satisfy the server's required-xKey rule (handlers_types.go).
func slugXKey(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case !prevUnderscore && b.Len() > 0:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func addProperty(t *testing.T, base, spaceID, typeID, name, kind string) string {
	t.Helper()
	var resp map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types/"+typeID+"/properties",
		fmt.Sprintf(`{"name":%q,"kind":%q}`, name, kind), http.StatusCreated, &resp)
	id, _ := resp["propId"].(string)
	if id == "" {
		t.Fatalf("addProperty %q/%q: propId empty: %+v", typeID, name, resp)
	}
	return id
}

func createObject(t *testing.T, base, spaceID, typeID string) string {
	t.Helper()
	var resp map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeID), http.StatusCreated, &resp)
	id, _ := resp["objectId"].(string)
	if id == "" {
		t.Fatalf("createObject: objectId empty: %+v", resp)
	}
	return id
}

// assertProp reads the object's properties and asserts record[typeId][propId]
// equals want. JSON numbers decode to float64 — pass float64 literals.
func assertProp(t *testing.T, base, spaceID, objectID, typeID, propID string, want any) {
	t.Helper()
	var got map[string]any
	mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID+"/properties/"+objectID, "",
		http.StatusOK, &got)
	record, _ := got["record"].(map[string]any)
	node, _ := record[typeID].(map[string]any)
	if node[propID] != want {
		t.Errorf("record[%s][%s] = %#v, want %#v; full=%+v", typeID, propID, node[propID], want, record)
	}
}

// mustErrorCode issues a request, asserts the status, and returns the
// error envelope's code. Fails if the body isn't the canonical
// {"error":{"code":...}} shape.
func mustErrorCode(t *testing.T, method, url, body string, wantStatus int) string {
	t.Helper()
	var env map[string]any
	mustJSON(t, method, url, body, wantStatus, &env)
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s: no error object in %+v", method, url, env)
	}
	code, _ := errObj["code"].(string)
	return code
}
