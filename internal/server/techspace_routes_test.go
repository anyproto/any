package server

import (
	"strings"
	"testing"
)

// TestTechAllowedRoutes_TypeDatasetSurface pins allowlist completeness
// for the type/dataset definition surface: a tech-space bundle root
// declares runtime datasets, so every registered
// `/types/:typeId/datasets…` route must be reachable on the tech
// space — a new one is tech-refused until it is listed.
func TestTechAllowedRoutes_TypeDatasetSurface(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	var seen int
	for _, r := range e.Routes() {
		if !strings.Contains(r.Path, "/types/:typeId/datasets") {
			continue
		}
		seen++
		if _, ok := techAllowedRoutes[r.Method+" "+r.Path]; !ok {
			t.Errorf("%s %s is registered but not tech-space allowed", r.Method, r.Path)
		}
	}
	if seen < 7 {
		t.Fatalf("expected the dataset surface (list/add/patch/remove + field add/patch/remove), saw %d routes", seen)
	}
}
