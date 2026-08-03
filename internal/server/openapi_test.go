//go:build !mobile

package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestServeOpenAPI pins the served spec artifact: OpenAPI 3.1, every
// route present, and additionalProperties: false stamped onto each
// strict request schema — the discovery-time counterpart of the
// 400 request.unknown_field contract. Runs against the real embedded
// docs, so a generator regression or a strictBodySchemas entry that
// stops matching the generated spec fails here, not on a client.
func TestServeOpenAPI(t *testing.T) {
	c, rec := newTestContext("")
	if err := serveOpenAPI(c); err != nil {
		t.Fatalf("serveOpenAPI: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("served spec is not valid JSON: %v", err)
	}
	if v, _ := spec["openapi"].(string); v != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", v)
	}
	paths, _ := spec["paths"].(map[string]any)
	if len(paths) < 100 {
		t.Errorf("paths = %d, expected the full route catalog (>= 100)", len(paths))
	}
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatal("no components.schemas in served spec")
	}
	for _, name := range strictBodySchemas {
		schema, ok := schemas[name].(map[string]any)
		if !ok {
			t.Errorf("strict schema %s missing", name)
			continue
		}
		if ap, ok := schema["additionalProperties"].(bool); !ok || ap {
			t.Errorf("%s additionalProperties = %v, want false", name, schema["additionalProperties"])
		}
	}
}
