package e2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

const overviewBundlePath = "system%3Aoverview%2Fv1"

// entries maps an Overview record id to its user fields.
type entries map[string]map[string]any

// overviewSetup runs the Overview setup on p, polling through the
// retryable 409s, and returns the one bundle it touched.
func overviewSetup(t *testing.T, p *peer, spaceId string) api.CatalogSetupBundle {
	t.Helper()
	var res api.CatalogSetupResponse
	if !pollUntil(3*time.Minute, func() bool {
		res = api.CatalogSetupResponse{}
		return tryJSON(t, http.MethodPost, p.base+"/v1/catalog/overview/setup",
			`{"spaceId":"`+spaceId+`"}`, &res) == http.StatusOK
	}) {
		t.Fatalf("%s: overview setup never answered 200", p.name)
	}
	if len(res.Bundles) != 1 || res.Bundles[0].Id != "system:overview/v1" || res.Bundles[0].Bundle.RootId == "" {
		t.Fatalf("%s: overview setup: %+v", p.name, res)
	}
	return res.Bundles[0]
}

// overviewEntries reads every record of root's `entries`. ok=false
// while the root's tree has not arrived (404 object.not_found).
func overviewEntries(t *testing.T, p *peer, spaceId, root string) (entries, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+spaceId+"/query",
		`{"objectId":"`+root+`","dataset":"`+root+`_entries","projection":{"_ver":-1}}`)
	if resp.StatusCode == http.StatusNotFound && errorCode(t, raw) == "object.not_found" {
		return nil, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: query %s_entries: status=%d body=%s", p.name, root, resp.StatusCode, raw)
	}
	var q struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatalf("%s: decode entries: %v body=%s", p.name, err, raw)
	}
	out := entries{}
	for _, r := range q.Records {
		id, _ := r["id"].(string)
		fields := map[string]any{}
		for k, v := range r {
			if k != "id" && !strings.HasPrefix(k, "_") {
				fields[k] = v
			}
		}
		out[id] = fields
	}
	return out, true
}

// upsertEntries writes records (a JSON array) into root's `entries`
// and fails on any rejection.
func upsertEntries(t *testing.T, p *peer, spaceId, root, records string) api.UpsertResult {
	t.Helper()
	var up api.UpsertResult
	mustJSON(t, http.MethodPost, p.base+"/v1/spaces/"+spaceId+"/upsert",
		`{"objectId":"`+root+`","dataset":"`+root+`_entries","records":`+records+`}`, http.StatusOK, &up)
	if len(up.Rejections) > 0 {
		t.Fatalf("%s: upsert rejected: %+v", p.name, up.Rejections)
	}
	return up
}

// containsEntries reports whether got holds every record of want with
// exactly want's fields (JSON numbers compare as float64).
func containsEntries(got, want entries) bool {
	for id, fields := range want {
		if !reflect.DeepEqual(got[id], fields) {
			return false
		}
	}
	return true
}

// mustEntries decodes a JSON object of id → fields, the shape every
// expectation below is written in.
func mustEntries(t *testing.T, s string) entries {
	t.Helper()
	var e entries
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		t.Fatalf("expectation %s: %v", s, err)
	}
	return e
}

// recordsJSON renders entries as an upsert records array.
func recordsJSON(t *testing.T, e entries) string {
	t.Helper()
	type rec struct {
		Id     string         `json:"id"`
		Fields map[string]any `json:"fields"`
	}
	var out []rec
	for id, f := range e {
		out = append(out, rec{Id: id, Fields: f})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal records: %v", err)
	}
	return string(raw)
}

// syncPeers reads the space's peer counts on p.
func syncPeers(t *testing.T, p *peer, spaceId string) api.SpaceSyncStatusResponse {
	t.Helper()
	var st api.SpaceSyncStatusResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+spaceId+"/sync-status", "", http.StatusOK, &st)
	return st
}

// assertOffline fails unless p syncs spaceId over no path at all.
func assertOffline(t *testing.T, p *peer, spaceId string) {
	t.Helper()
	if st := syncPeers(t, p, spaceId); st.NetworkPeers != 0 || st.LocalPeers != 0 || st.GlobalPeers != 0 {
		t.Fatalf("%s is not offline: %+v", p.name, st)
	}
}

// restartOffline restarts p with every node address dead and LAN p2p
// off. any-sync prefers the stored nodeconf over the configured one, so
// the stored copy is moved aside too; restore puts p back online.
func restartOffline(t *testing.T, bin string, p *peer) (restore func()) {
	t.Helper()
	online, err := os.ReadFile(absStagingPath(t))
	if err != nil {
		t.Fatalf("read nodeconf: %v", err)
	}
	netId, err := config.NodeconfNetworkId(online)
	if err != nil {
		t.Fatalf("nodeconf network id: %v", err)
	}
	var conf map[string]any
	if err := yaml.Unmarshal(online, &conf); err != nil {
		t.Fatalf("parse nodeconf: %v", err)
	}
	nodes, _ := conf["nodes"].([]any)
	for _, n := range nodes {
		n.(map[string]any)["addresses"] = []string{"127.0.0.1:1"}
	}
	dead, err := yaml.Marshal(conf)
	if err != nil {
		t.Fatalf("marshal nodeconf: %v", err)
	}
	deadPath := filepath.Join(p.dataDir, "nodeconf-offline.yml")
	if err := os.WriteFile(deadPath, dead, 0o600); err != nil {
		t.Fatalf("write offline nodeconf: %v", err)
	}

	p.stop(t)
	// Written only once the coordinator serves a newer conf.
	stored, _ := filepath.Glob(filepath.Join(p.dataDir, "*", "sdk", "anysync", "nodeconf", netId+".yml"))
	saved := map[string][]byte{}
	for _, path := range stored {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read stored nodeconf: %v", err)
		}
		saved[path] = raw
		if err := os.Remove(path); err != nil {
			t.Fatalf("move stored nodeconf aside: %v", err)
		}
	}
	bootWithConfig(t, bin, p, fmt.Sprintf("network:\n  nodeconfPath: %s\nindex:\n  embedder: none\np2p:\n  enabled: false\n", deadPath))

	return func() {
		t.Helper()
		p.stop(t)
		for path, raw := range saved {
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatalf("restore stored nodeconf: %v", err)
			}
		}
		bootWithConfig(t, bin, p, fmt.Sprintf("network:\n  nodeconfPath: %s\nindex:\n  embedder: none\n", absStagingPath(t)))
	}
}

// bootWithConfig starts p's server again on its data dir and address.
func bootWithConfig(t *testing.T, bin string, p *peer, cfg string) {
	t.Helper()
	cfgPath := filepath.Join(p.dataDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	p.srv = execServer(t, bin, cfgPath, p.dataDir, p.addr)
	waitForReady(t, p.addr, 60*time.Second)
}

// TestE2E_MultipeerOverviewConcurrentEdits proves the Overview's merge
// across two writers of one space: each edits it while partitioned from
// the other, and after sync both hold every entry either side wrote,
// with an entry both touched merged per field.
func TestE2E_MultipeerOverviewConcurrentEdits(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer overview test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	writer := startPeer(t, bin, "writer")
	defer writer.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"overview"}`, http.StatusCreated, &sp)
	ov := overviewSetup(t, owner, sp.Id)
	if !ov.Installed || ov.Bundle.Derived || ov.TypeId != ov.Bundle.RootId {
		t.Fatalf("owner setup: %+v", ov)
	}
	root := ov.Bundle.RootId
	upsertEntries(t, owner, sp.Id, root, `[
		{"id":"settings","fields":{"opensTo":"overview"}},
		{"id":"w:chat","fields":{"pos":"PPPO","layout":"messages"}}]`)

	joinSpace(t, owner, writer, sp.Id, api.SpacePermissionWriter)
	adopted := overviewSetup(t, writer, sp.Id)
	if adopted.Installed || adopted.Bundle.RootId != root || len(adopted.Bundle.Losers) != 0 {
		t.Fatalf("writer setup must adopt %s: %+v", root, adopted)
	}
	base := mustEntries(t, `{
		"settings": {"opensTo":"overview"},
		"w:chat":   {"pos":"PPPO","layout":"messages"}}`)
	if !pollUntilSynced(t, 2*time.Minute, sp.Id, []*peer{owner, writer}, func() bool {
		got, ok := overviewEntries(t, writer, sp.Id, root)
		return ok && containsEntries(got, base)
	}) {
		t.Fatal("the owner's entries never reached the writer")
	}

	// Partition: the writer runs with no node and no LAN path.
	restore := restartOffline(t, bin, writer)
	assertOffline(t, writer, sp.Id)

	// Owner: a move, an add, one field on each shared record, and the
	// first half of a widget both sides add.
	upsertEntries(t, owner, sp.Id, root, `[
		{"id":"s:collections","fields":{"pos":"PPPA"}},
		{"id":"w:tasks","fields":{"pos":"PPPB","removed":false}},
		{"id":"settings","fields":{"editRowHidden":true}},
		{"id":"w:chat","fields":{"limit":7}},
		{"id":"w:wiki","fields":{"pos":"PPPC","layout":"tree"}},
		{"id":"s:recent","fields":{"pos":"PPPD"}}]`)
	// Writer: a different move, a folder with a collection in it, other
	// fields on the same records, the other half of the shared widget,
	// and a competing move of s:recent.
	upsertEntries(t, writer, sp.Id, root, `[
		{"id":"s:bin","fields":{"pos":"PPPE"}},
		{"id":"f:wfolder","fields":{"name":"Writer folder","parentId":"","pos":"a0"}},
		{"id":"t:wcollection","fields":{"parentId":"f:wfolder","pos":"a0"}},
		{"id":"settings","fields":{"opensWith":"wobject"}},
		{"id":"w:chat","fields":{"layout":"compact"}},
		{"id":"w:wiki","fields":{"limit":3,"removed":false}},
		{"id":"s:recent","fields":{"pos":"PPPF"}}]`)

	// Neither side has seen the other's change.
	if got, _ := overviewEntries(t, owner, sp.Id, root); got["f:wfolder"] != nil || got["settings"]["opensWith"] != nil {
		t.Fatalf("the owner saw the writer's partitioned change: %v", got)
	}
	if got, _ := overviewEntries(t, writer, sp.Id, root); got["w:tasks"] != nil || got["settings"]["editRowHidden"] != nil {
		t.Fatalf("the writer saw the owner's partitioned change: %v", got)
	}
	assertOffline(t, writer, sp.Id)

	restore()
	want := mustEntries(t, `{
		"settings":      {"opensTo":"overview","editRowHidden":true,"opensWith":"wobject"},
		"w:chat":        {"pos":"PPPO","layout":"compact","limit":7},
		"w:wiki":        {"pos":"PPPC","layout":"tree","limit":3,"removed":false},
		"w:tasks":       {"pos":"PPPB","removed":false},
		"s:collections": {"pos":"PPPA"},
		"s:bin":         {"pos":"PPPE"},
		"f:wfolder":     {"name":"Writer folder","parentId":"","pos":"a0"},
		"t:wcollection": {"parentId":"f:wfolder","pos":"a0"}}`)
	var onOwner, onWriter entries
	start := time.Now()
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{writer, owner}, func() bool {
		onOwner, _ = overviewEntries(t, owner, sp.Id, root)
		onWriter, _ = overviewEntries(t, writer, sp.Id, root)
		return containsEntries(onOwner, want) && containsEntries(onWriter, want) &&
			reflect.DeepEqual(onOwner["s:recent"], onWriter["s:recent"])
	}) {
		t.Fatalf("the Overview did not converge:\n owner=%v\n writer=%v", onOwner, onWriter)
	}
	t.Logf("converged %v after reconnect; s:recent pos=%v (owner wrote PPPD, writer PPPF)",
		time.Since(start).Round(time.Millisecond), onOwner["s:recent"]["pos"])
	if len(onOwner) != len(want)+1 || !maps.EqualFunc(onOwner, onWriter, func(a, b map[string]any) bool { return reflect.DeepEqual(a, b) }) {
		t.Fatalf("the two replicas differ or hold extra entries:\n owner=%v\n writer=%v", onOwner, onWriter)
	}
}
