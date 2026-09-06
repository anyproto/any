// Property PATCH/DELETE e2e: drives the generic property-patch surface
// (PatchProperty) and property removal (RemoveProperty) end-to-end
// through the HTTP server against the real SDK: rename, delete, choice
// options + colors + order as xFormat paths, the leaf-only rule, the
// slug ↔ kind rule. See docs/03-api.md § Types, docs/27-descriptors.md.
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestE2E_PropertyPatch(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr
	spaceID := createSpace(t, base, "PropPatch", "property patch e2e")
	taskType := createType(t, base, spaceID, "Task")
	propsURL := base + "/v1/spaces/" + spaceID + "/types/" + taskType + "/properties"

	// A many-valued choice property.
	var addResp map[string]any
	mustJSON(t, http.MethodPost, propsURL,
		`{"name":"Tags","xKey":"tags","kind":"array","xFormat":{"type":"choice","config":{"multiple":true}}}`,
		http.StatusCreated, &addResp)
	tagsProp, _ := addResp["propId"].(string)
	if tagsProp == "" {
		t.Fatalf("no propId in %+v", addResp)
	}
	patchURL := propsURL + "/" + tagsProp

	// descriptorOf reads the property back and returns its xFormat.
	descriptorOf := func(propId string) map[string]any {
		t.Helper()
		var listed map[string]any
		mustJSON(t, http.MethodGet, propsURL, "", http.StatusOK, &listed)
		props, _ := listed["properties"].([]any)
		for _, p := range props {
			pm, _ := p.(map[string]any)
			if pm["id"] == propId {
				xf, _ := pm["xFormat"].(map[string]any)
				return xf
			}
		}
		t.Fatalf("prop %s not in list", propId)
		return nil
	}
	// optionsOf returns the choice property's xFormat.options map.
	optionsOf := func() map[string]any {
		t.Helper()
		opts, _ := descriptorOf(tagsProp)["options"].(map[string]any)
		return opts
	}

	t.Run("add option (atomic multi-field set) round-trips", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL,
			`{"set":{"xFormat.options.high.name":"High","xFormat.options.high.color":"red","xFormat.options.high.pos":"a0"}}`,
			http.StatusNoContent)
		opts := optionsOf()
		high, _ := opts["high"].(map[string]any)
		if high["name"] != "High" || high["color"] != "red" || high["pos"] != "a0" {
			t.Errorf("option not round-tripped: %+v", high)
		}
	})

	t.Run("recolor single leaf", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL,
			`{"set":{"xFormat.options.high.color":"crimson"}}`, http.StatusNoContent)
		high, _ := optionsOf()["high"].(map[string]any)
		if high["color"] != "crimson" || high["name"] != "High" {
			t.Errorf("recolor lost sibling leaves: %+v", high)
		}
	})

	t.Run("delete option then re-add same key", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL,
			`{"unset":["xFormat.options.high"]}`, http.StatusNoContent)
		if _, ok := optionsOf()["high"]; ok {
			t.Errorf("option not deleted")
		}
		mustStatus(t, http.MethodPatch, patchURL,
			`{"set":{"xFormat.options.high.name":"Highest"}}`, http.StatusNoContent)
		high, _ := optionsOf()["high"].(map[string]any)
		if high["name"] != "Highest" {
			t.Errorf("re-added option = %+v", high)
		}
	})

	t.Run("rename property", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL, `{"set":{"name":"Labels"}}`, http.StatusNoContent)
		var listed map[string]any
		mustJSON(t, http.MethodGet, propsURL, "", http.StatusOK, &listed)
		props, _ := listed["properties"].([]any)
		for _, p := range props {
			pm, _ := p.(map[string]any)
			if pm["id"] == tagsProp && pm["name"] != "Labels" {
				t.Errorf("rename failed: name=%v", pm["name"])
			}
		}
	})

	t.Run("pinned path rejected 400 property.immutable", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPatch, patchURL,
			`{"set":{"kind":"number"}}`, http.StatusBadRequest)
		if code != "property.immutable" {
			t.Errorf("code = %q, want property.immutable", code)
		}
	})

	t.Run("slug moves within the kind only", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL, `{"set":{"xFormat.type":"relation"}}`, http.StatusNoContent)
		if descriptorOf(tagsProp)["type"] != "relation" {
			t.Errorf("slug not moved: %+v", descriptorOf(tagsProp))
		}
		mustStatus(t, http.MethodPatch, patchURL, `{"set":{"xFormat.type":"choice"}}`, http.StatusNoContent)
		code := mustErrorCode(t, http.MethodPatch, patchURL,
			`{"set":{"xFormat.type":"text"}}`, http.StatusBadRequest)
		if code != "property.format_invalid" {
			t.Errorf("code = %q, want property.format_invalid", code)
		}
	})

	t.Run("empty patch rejected", func(t *testing.T) {
		mustStatus(t, http.MethodPatch, patchURL, `{}`, http.StatusBadRequest)
	})

	t.Run("set bare options container rejected (no clobber)", func(t *testing.T) {
		// Setting the whole options map — to a string or an object —
		// must not wipe the option set.
		for _, body := range []string{`{"set":{"xFormat.options":"x"}}`, `{"set":{"xFormat.options":{"low":{"name":"Low"}}}}`} {
			code := mustErrorCode(t, http.MethodPatch, patchURL, body, http.StatusBadRequest)
			if code != "request.invalid_field" {
				t.Errorf("%s: code = %q, want request.invalid_field", body, code)
			}
		}
		if _, ok := optionsOf()["high"]; !ok {
			t.Errorf("option set clobbered")
		}
	})

	t.Run("non-string value on scalar field → request.invalid_field", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPatch, patchURL,
			`{"set":{"name":42}}`, http.StatusBadRequest)
		if code != "request.invalid_field" {
			t.Errorf("code = %q, want request.invalid_field", code)
		}
	})

	t.Run("descriptor grows onto a bare property; retired paths reject", func(t *testing.T) {
		var pr map[string]any
		mustJSON(t, http.MethodPost, propsURL, `{"name":"Plain","xKey":"plain","kind":"string"}`, http.StatusCreated, &pr)
		plainProp, _ := pr["propId"].(string)
		if descriptorOf(plainProp) != nil {
			t.Errorf("bare property must read back without a descriptor")
		}
		mustStatus(t, http.MethodPatch, propsURL+"/"+plainProp,
			`{"set":{"xFormat.type":"email","xFormat.icon":"envelope"}}`, http.StatusNoContent)
		if xf := descriptorOf(plainProp); xf["type"] != "email" || xf["icon"] != "envelope" {
			t.Errorf("grown descriptor = %+v", xf)
		}
		code := mustErrorCode(t, http.MethodPatch, propsURL+"/"+plainProp,
			`{"set":{"format.ui":"select"}}`, http.StatusBadRequest)
		if code != "request.invalid_field" {
			t.Errorf("code = %q, want request.invalid_field", code)
		}
	})

	t.Run("remove property then it's gone", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, patchURL, "", http.StatusNoContent)
		var listed map[string]any
		mustJSON(t, http.MethodGet, propsURL, "", http.StatusOK, &listed)
		props, _ := listed["properties"].([]any)
		for _, p := range props {
			pm, _ := p.(map[string]any)
			if pm["id"] == tagsProp {
				t.Errorf("property still listed after delete")
			}
		}
	})

	t.Run("remove unknown propId 404", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodDelete,
			propsURL+"/no-such-prop", "", http.StatusNotFound)
		if code != "sdk.not_found" {
			t.Errorf("code = %q, want sdk.not_found", code)
		}
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
