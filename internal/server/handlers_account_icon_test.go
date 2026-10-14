package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// fakePNG is n bytes that sniff as image/png: the PNG signature over
// deterministic noise. The server never decodes the image.
func fakePNG(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(7)).Read(b)
	copy(b, "\x89PNG\r\n\x1a\n")
	return b
}

// TestServer_AccountIcon drives the avatar surface against the
// in-process handler with a real SDK: upload → metadata carries the
// cid → own download (full + Range) → a name update keeps it → delete.
// Validation refusals (not an image, too large) leave the stored
// avatar untouched.
func TestServer_AccountIcon(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	t.Run("no avatar yet", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodGet, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != api.ErrAccountIconNotFound {
			t.Errorf("code = %q, want %s", env.Error.Code, api.ErrAccountIconNotFound)
		}
	})

	t.Run("not an image", func(t *testing.T) {
		for name, body := range map[string][]byte{
			"text":        []byte("hello"),
			"html as png": []byte("<script>alert(1)</script>"),
			"svg":         []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		} {
			// The declared type is never trusted.
			rec := doRaw(t, e, http.MethodPut, "/v1/account/icon?name=a.png", "image/png", body)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s: status = %d, want 415; body=%s", name, rec.Code, rec.Body.String())
			}
			var env api.ErrorEnvelope
			_ = json.Unmarshal(rec.Body.Bytes(), &env)
			if env.Error.Code != api.ErrAccountIconNotImage {
				t.Errorf("%s: code = %q, want %s", name, env.Error.Code, api.ErrAccountIconNotImage)
			}
		}
	})

	t.Run("empty body", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodPut, "/v1/account/icon", "image/png", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("too large", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodPut, "/v1/account/icon", "image/png", fakePNG(space.MaxIconSize+1))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != api.ErrAccountIconTooLarge {
			t.Errorf("code = %q, want %s", env.Error.Code, api.ErrAccountIconTooLarge)
		}
		var acct api.AccountResponse
		rec = doJSON(t, e, http.MethodGet, "/v1/account", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &acct)
		if acct.Metadata != nil && acct.Metadata.IconCID != "" {
			t.Errorf("a refused upload must not set an avatar: %+v", acct.Metadata)
		}
	})

	// Under the inline cutoff on purpose: the avatar path must still
	// mint a content address.
	img := fakePNG(1500)
	var meta api.AccountMetadata
	t.Run("upload", func(t *testing.T) {
		// Content sniffed: no Content-Type, no extension in the name.
		rec := doRaw(t, e, http.MethodPut, "/v1/account/icon?name=me", "", img)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if meta.IconCID == "" {
			t.Fatalf("reply carries no iconCid: %s", rec.Body.String())
		}
		var acct api.AccountResponse
		rec = doJSON(t, e, http.MethodGet, "/v1/account", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &acct)
		if acct.Metadata == nil || acct.Metadata.IconCID != meta.IconCID {
			t.Errorf("GET /v1/account metadata = %+v, want iconCid %s", acct.Metadata, meta.IconCID)
		}
	})

	t.Run("download", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodGet, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}
		if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
		}
		if v := rec.Header().Get("Content-Security-Policy"); v != "default-src 'none'; sandbox" {
			t.Errorf("Content-Security-Policy = %q", v)
		}
		if v := rec.Header().Get("Content-Disposition"); v != "inline" {
			t.Errorf("Content-Disposition = %q, want inline", v)
		}
		if !bytes.Equal(rec.Body.Bytes(), img) {
			t.Errorf("body mismatch: %d bytes, want %d", rec.Body.Len(), len(img))
		}

		r := httptest.NewRequest(http.MethodGet, "/v1/account/icon", nil)
		r.Header.Set("Range", "bytes=8-15")
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, r)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("Range status = %d, want 206", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), img[8:16]) {
			t.Errorf("Range body mismatch")
		}

		// The own identity resolves through the directory route too.
		rec = doRaw(t, e, http.MethodGet, "/v1/identities/"+d.sdk.Account().Id()+"/icon", "", nil)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), img) {
			t.Errorf("identities icon status = %d, %d bytes", rec.Code, rec.Body.Len())
		}
	})

	t.Run("name update keeps the avatar", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPut, "/v1/account/metadata", `{"name":"Alice"}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		var acct api.AccountResponse
		rec = doJSON(t, e, http.MethodGet, "/v1/account", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &acct)
		if acct.Metadata == nil || acct.Metadata.Name != "Alice" || acct.Metadata.IconCID != meta.IconCID {
			t.Errorf("metadata = %+v, want name Alice + iconCid %s", acct.Metadata, meta.IconCID)
		}
	})

	t.Run("replace", func(t *testing.T) {
		img2 := fakePNG(2500)
		rec := doRaw(t, e, http.MethodPut, "/v1/account/icon", "image/png", img2)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var meta2 api.AccountMetadata
		_ = json.Unmarshal(rec.Body.Bytes(), &meta2)
		if meta2.IconCID == "" || meta2.IconCID == meta.IconCID {
			t.Errorf("replace iconCid = %q, want a new cid", meta2.IconCID)
		}
		rec = doRaw(t, e, http.MethodGet, "/v1/account/icon", "", nil)
		if got, _ := io.ReadAll(rec.Body); !bytes.Equal(got, img2) {
			t.Errorf("download after replace returns the old bytes")
		}
	})

	t.Run("delete", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodDelete, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		rec = doRaw(t, e, http.MethodGet, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status after delete = %d, want 404", rec.Code)
		}
		var acct api.AccountResponse
		rec = doJSON(t, e, http.MethodGet, "/v1/account", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &acct)
		if acct.Metadata == nil || acct.Metadata.IconCID != "" || acct.Metadata.Name != "Alice" {
			t.Errorf("metadata after delete = %+v, want name kept and no iconCid", acct.Metadata)
		}
		// Idempotent.
		rec = doRaw(t, e, http.MethodDelete, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusNoContent {
			t.Errorf("second delete status = %d, want 204", rec.Code)
		}
	})

	// The serve-side defence: bytes a contact chose that are not an
	// image never come back with an active type. Stored through the
	// SDK directly, as a modified client would.
	t.Run("non-image bytes serve inert", func(t *testing.T) {
		html := []byte("<!doctype html><script>alert(1)</script>")
		if _, err := d.sdk.Account().SetIcon(context.Background(), bytes.NewReader(html), space.IconOpts{Name: "x.png"}); err != nil {
			t.Fatalf("SetIcon: %v", err)
		}
		rec := doRaw(t, e, http.MethodGet, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", ct)
		}
		if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
		}
		if !bytes.Equal(rec.Body.Bytes(), html) {
			t.Errorf("body mismatch")
		}
		rec = doRaw(t, e, http.MethodDelete, "/v1/account/icon", "", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("cleanup delete status = %d", rec.Code)
		}
	})

	t.Run("unknown identity", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodGet, "/v1/identities/A00neverEncountered/icon", "", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != api.ErrIdentityIconNotFound {
			t.Errorf("code = %q, want %s", env.Error.Code, api.ErrIdentityIconNotFound)
		}
	})
}
