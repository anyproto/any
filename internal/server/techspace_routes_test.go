package server

import (
	"strings"
	"testing"
)

// TestTechAllowedRoutes_TypeDatasetSurface pins allowlist completeness
// for the type/part/dataset definition surface: a tech-space bundle
// root declares parts, so every registered `/types/:typeId/parts…` and
// `/types/:typeId/datasets…` route must be reachable on the tech space
// — a new one is tech-refused until it is listed.
func TestTechAllowedRoutes_TypeDatasetSurface(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	var seen int
	for _, r := range e.Routes() {
		if !strings.Contains(r.Path, "/types/:typeId/datasets") && !strings.Contains(r.Path, "/types/:typeId/parts") {
			continue
		}
		seen++
		if _, ok := techAllowedRoutes[r.Method+" "+r.Path]; !ok {
			t.Errorf("%s %s is registered but not tech-space allowed", r.Method, r.Path)
		}
	}
	if seen < 11 {
		t.Fatalf("expected the parts surface (list/add/patch/remove + dataset add) and the dataset surface (list/patch/remove + field add/patch/remove), saw %d routes", seen)
	}
}
