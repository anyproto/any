package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

const unsupportedMediaType = "request.unsupported_media_type"

func doBody(e http.Handler, method, path, contentType, origin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, rec.Body.String())
	}
	return env.Error.Code
}

// An unbooted server is enough here: the check runs ahead of the auth
// guard, so 401 auth.required means the request got past it.
func TestRequireJSONBody(t *testing.T) {
	e := buildEcho(&deps{})
	const body = `{"objectId":"o1","recordIds":["r1"]}`
	const write = "/v1/spaces/s1/delete-records"

	for _, tc := range []struct {
		name, method, path, contentType, origin, body string
		wantStatus                                    int
		wantCode                                      string
	}{
		{"json", http.MethodPost, write, "application/json", "", body, 401, "auth.required"},
		{"json with charset", http.MethodPost, write, "application/json; charset=utf-8", "", body, 401, "auth.required"},
		{"json in another case", http.MethodPost, write, "Application/JSON", "", body, 401, "auth.required"},

		{"missing", http.MethodPost, write, "", "", body, 415, unsupportedMediaType},
		{"text", http.MethodPost, write, "text/plain", "", body, 415, unsupportedMediaType},
		{"form, the curl -d default", http.MethodPost, write, "application/x-www-form-urlencoded", "", body, 415, unsupportedMediaType},
		{"json as a parameter", http.MethodPost, write, "text/plain; application/json", "", body, 415, unsupportedMediaType},
		{"another json type", http.MethodPost, write, "application/problem+json", "", body, 415, unsupportedMediaType},
		{"modify", http.MethodPost, "/v1/spaces/s1/modify", "text/plain", "", body, 415, unsupportedMediaType},
		{"auth, outside the guard", http.MethodPost, "/v1/auth", "text/plain", "", `{}`, 415, unsupportedMediaType},
		{"put", http.MethodPut, "/v1/devices/me", "text/plain", "", body, 415, unsupportedMediaType},
		{"foreign origin", http.MethodPost, "/v1/spaces", "text/plain", "https://example.com", `{"name":"x"}`, 415, unsupportedMediaType},

		{"no body", http.MethodPost, write, "", "", "", 401, "auth.required"},
		{"no body, foreign type", http.MethodPost, write, "text/plain", "", "", 401, "auth.required"},
		{"read with a body", http.MethodGet, "/v1/spaces", "text/plain", "", body, 401, "auth.required"},

		{"raw: file attach", http.MethodPost, "/v1/spaces/s1/objects/o1/files", "image/jpeg", "", "bytes", 401, "auth.required"},
		{"raw: avatar", http.MethodPut, "/v1/account/icon", "image/png", "", "bytes", 401, "auth.required"},
		{"raw: local import", http.MethodPost, "/v1/local/import", "application/gzip", "", "bytes", 401, "auth.required"},

		{"unmatched route", http.MethodPost, "/v1/nope", "text/plain", "", body, 404, "request.not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doBody(e, tc.method, tc.path, tc.contentType, tc.origin, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tc.wantCode {
				t.Fatalf("code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

// echo's binder decodes a form body to an empty struct, so the check has
// to keep it away: "objectId required" means only a missing field.
func TestDeleteRecords_ContentType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	spaceId, _, objectId := setupSubscribeFixture(t, e)
	path := "/v1/spaces/" + spaceId + "/delete-records"
	body := `{"objectId":"` + objectId + `","recordIds":["r1"]}`

	rec := doBody(e, http.MethodPost, path, "application/x-www-form-urlencoded", "", body)
	if rec.Code != http.StatusUnsupportedMediaType || errorCode(t, rec) != unsupportedMediaType {
		t.Fatalf("form body: %d %s", rec.Code, rec.Body.String())
	}

	rec = doBody(e, http.MethodPost, path, "application/json", "", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "objectId required") {
		t.Fatalf("missing objectId: %d %s", rec.Code, rec.Body.String())
	}

	rec = doBody(e, http.MethodPost, path, "application/json", "", body)
	if rec.Code == http.StatusUnsupportedMediaType || strings.Contains(rec.Body.String(), "objectId required") {
		t.Fatalf("json body: %d %s", rec.Code, rec.Body.String())
	}
}
