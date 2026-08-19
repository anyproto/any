package server

import (
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
)

// activeDevicesMap defers the per-slug winner to the SDK's canonical
// space.ActiveDevice; these tests pin the map assembly around it —
// slug collection, dangling claims yielding no entry, independent
// slugs — not the tiebreak matrix (that lives in the SDK's tests).
func TestActiveDevicesMap(t *testing.T) {
	installed := map[string]any{}
	devices := []space.Device{
		{
			PeerId: "peerA",
			Apps:   map[string]map[string]any{"bao": installed, "other": installed},
			ActiveClaims: map[string]space.DeviceClaim{
				"bao":   {Seq: 1, At: 10},
				"other": {Seq: 5, At: 10},
			},
		},
		{
			PeerId: "peerB",
			Apps:   map[string]map[string]any{"bao": installed},
			ActiveClaims: map[string]space.DeviceClaim{
				"bao": {Seq: 2, At: 10},
				// Dangling: claim without the app installed anywhere else.
				"ghost": {Seq: 9, At: 99},
			},
		},
	}
	active := activeDevicesMap(devices)
	want := map[string]string{"bao": "peerB", "other": "peerA"}
	if len(active) != len(want) {
		t.Fatalf("active = %v, want %v", active, want)
	}
	for slug, peer := range want {
		if active[slug] != peer {
			t.Errorf("active[%q] = %q, want %q", slug, active[slug], peer)
		}
	}
	if _, ok := active["ghost"]; ok {
		t.Error("dangling claim produced a winner")
	}
}

func TestActiveDevicesMapEmpty(t *testing.T) {
	if got := activeDevicesMap(nil); got != nil {
		t.Errorf("activeDevicesMap(nil) = %v, want nil", got)
	}
	noClaims := []space.Device{{PeerId: "peerA", Apps: map[string]map[string]any{"bao": {}}}}
	if got := activeDevicesMap(noClaims); got != nil {
		t.Errorf("activeDevicesMap(no claims) = %v, want nil", got)
	}
}
