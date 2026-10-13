package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerAvatar proves the avatar reaches a contact: the
// owner uploads an image, the joiner's directory row gains the owner's
// iconCid once the file is backed up, and the joiner downloads the
// bytes by content address through the public read path — never
// through the owner's space — then sees the avatar removed.
//
// Gated like the files latency benchmark: a local-infra nodeconf with
// fileV2 nodes (staging grants fresh accounts no file quota, so an
// upload there never becomes durable). The local object store is
// private, so the public read surface is a stand-in that serves the
// owner's own CARv2 files — the very bytes the node holds — and the
// joiner is pointed at it as its public base.
func TestE2E_MultipeerAvatar(t *testing.T) {
	nodeconf := filesLatencyNodeconf(t)
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeerConf(t, bin, "owner", nodeconf)
	defer owner.stop(t)

	standin := newCarStandin(t, owner.dataDir)
	defer standin.Close()
	t.Setenv("ANY_FILES_PUBLIC_READ_BASE_URL", standin.URL)
	joiner := startPeerConf(t, bin, "joiner", nodeconf)
	defer joiner.stop(t)

	var ownerAcct api.AccountResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/account", "", http.StatusOK, &ownerAcct)

	img := make([]byte, 3000)
	rand.New(rand.NewSource(146)).Read(img)
	copy(img, "\x89PNG\r\n\x1a\n")

	mustStatus(t, http.MethodPut, owner.base+"/v1/account/metadata", `{"name":"Owner Alice"}`, http.StatusNoContent)
	var meta api.AccountMetadata
	putRaw(t, owner.base+"/v1/account/icon?name=me.png", "image/png", img, http.StatusOK, &meta)
	if meta.IconCID == "" {
		t.Fatalf("PUT /v1/account/icon returned no iconCid: %+v", meta)
	}

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"avatar-shared"}`, http.StatusCreated, &sp)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// The reference is published once the file is durable, and the
	// joiner's watcher re-pulls profiles on its own tick.
	var row api.IdentityInfo
	if !pollUntil(240*time.Second, func() bool {
		resp, raw := doRequest(t, http.MethodGet, joiner.base+"/v1/identities/"+ownerAcct.Id, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		row = api.IdentityInfo{}
		_ = json.Unmarshal(raw, &row)
		return row.IconCID == meta.IconCID
	}) {
		t.Fatalf("joiner never saw the owner's iconCid; last=%+v", row)
	}

	resp, raw := doRequest(t, http.MethodGet, joiner.base+"/v1/identities/"+ownerAcct.Id+"/icon", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/identities/:id/icon status=%d body=%s", resp.StatusCode, raw)
	}
	if !bytes.Equal(raw, img) {
		t.Fatalf("joiner downloaded %d bytes, want the %d-byte avatar", len(raw), len(img))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if standin.requests() == 0 {
		t.Errorf("the joiner never fetched through the public read path")
	}

	mustStatus(t, http.MethodDelete, owner.base+"/v1/account/icon", "", http.StatusNoContent)
	if !pollUntil(120*time.Second, func() bool {
		resp, raw := doRequest(t, http.MethodGet, joiner.base+"/v1/identities/"+ownerAcct.Id, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		row = api.IdentityInfo{}
		_ = json.Unmarshal(raw, &row)
		return row.Name == "Owner Alice" && row.IconCID == ""
	}) {
		t.Fatalf("joiner never saw the avatar removed; last=%+v", row)
	}
	mustStatus(t, http.MethodGet, joiner.base+"/v1/identities/"+ownerAcct.Id+"/icon", "", http.StatusNotFound)
}

// carStandin plays the public read surface over a private object
// store: /blob/{spaceId}/{rootCid} answers with the CARv2 the owner
// peer wrote for that root, which is byte for byte the object it
// uploaded. Range requests are honoured, as a real object store would.
type carStandin struct {
	*httptest.Server
	n *int32
}

func newCarStandin(t *testing.T, dataDir string) *carStandin {
	var n int32
	s := &carStandin{n: &n}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "blob" {
			http.NotFound(w, r)
			return
		}
		path := findCar(dataDir, parts[1], parts[2])
		if path == "" {
			t.Logf("standin: no CAR for %s/%s under %s", parts[1], parts[2], dataDir)
			http.NotFound(w, r)
			return
		}
		*s.n++
		http.ServeFile(w, r, path)
	}))
	return s
}

func (s *carStandin) requests() int { return int(*s.n) }

// findCar walks dataDir for <spaceId>/<shard>/<rootCid>.car.
func findCar(dataDir, spaceId, rootCid string) string {
	var found string
	_ = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && d.Name() == rootCid+".car" && strings.Contains(path, string(os.PathSeparator)+spaceId+string(os.PathSeparator)) {
			found = path
		}
		return nil
	})
	return found
}

// putRaw sends body as the raw request body with the given content
// type and decodes the JSON reply into out.
func putRaw(t *testing.T, url, contentType string, body []byte, wantStatus int, out any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("PUT %s status=%d want=%d body=%s", url, resp.StatusCode, wantStatus, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s: %v (%s)", url, err, raw)
		}
	}
}
