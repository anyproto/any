package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerBundles proves the bundles contract across two
// peers: the owner registers a chat bundle, the joiner ADOPTS the same
// root off its own Ensure — no id exchange, the registry row syncs with
// the space — and messages sent by both peers into that root converge.
// This is what the registry exists for: the joiner never mints a chat
// of its own, so the space keeps exactly one conversation.
func TestE2E_MultipeerBundles(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer bundles test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"bundles"}`, http.StatusCreated, &sp)

	const bundleId = "notes/v1"
	var ensureBody = `{"id":"` + bundleId + `","name":"General","parts":` + modulePartsBody("editor") + `}`

	var installed api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/bundles",
		ensureBody, http.StatusOK, &installed)
	if !installed.Installed || installed.Bundle.RootId == "" {
		t.Fatalf("owner ensure did not install: %+v", installed)
	}
	if installed.Bundle.Name != "General" {
		t.Fatalf("name not stored: %+v", installed.Bundle)
	}

	// Owner writes first, before the joiner exists in the space.
	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + installed.Bundle.RootId
	ownerMd := ownerBase + "/editor/editor_blocks/markdown"
	if res := putMarkdown(t, ownerMd, "hello from owner"); len(res.Inserted) != 1 {
		t.Fatalf("owner write: %+v", res)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// The joiner ensures the SAME bundle and must adopt: the registry
	// row and the root's tree reach it with the space. Poll — right
	// after join neither has necessarily arrived, and until the root is
	// local the server refuses to hand out an id that cannot be written
	// (409 bundle.not_ready).
	var adopted api.BundleEnsureResponse
	if !pollUntil(2*time.Minute, func() bool {
		adopted = api.BundleEnsureResponse{}
		code := tryJSON(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/bundles", ensureBody, &adopted)
		return code == http.StatusOK && adopted.Bundle.RootId != ""
	}) {
		t.Fatalf("joiner never resolved the bundle root: %+v", adopted)
	}
	if adopted.Installed {
		t.Fatalf("joiner installed a competing root %q instead of adopting %q",
			adopted.Bundle.RootId, installed.Bundle.RootId)
	}
	if adopted.Bundle.RootId != installed.Bundle.RootId {
		t.Fatalf("bundle root diverged: owner=%q joiner=%q",
			installed.Bundle.RootId, adopted.Bundle.RootId)
	}

	// The row is also readable by id, percent-encoded in the path.
	var got api.BundleGetResponse
	mustJSON(t, http.MethodGet,
		joiner.base+"/v1/spaces/"+sp.Id+"/bundles/"+url.PathEscape(bundleId),
		"", http.StatusOK, &got)
	if got.Bundle.RootId != installed.Bundle.RootId || got.Bundle.Name != "General" {
		t.Fatalf("joiner GET = %+v", got)
	}

	// Setup children derive from the winner, so both peers reach the
	// same object without exchanging its id either. A child binds to
	// its parent's tree, so the joiner polls through
	// 409 bundle.not_ready until the winner's tree has landed.
	childBody := `{"seed":"memory/v1","types":["agent_memory"]}`
	childPath := "/v1/spaces/" + sp.Id + "/bundles/" + url.PathEscape(bundleId) + "/children"
	var ownerChild, joinerChild api.BundleChildResponse
	mustJSON(t, http.MethodPost, owner.base+childPath, childBody, http.StatusOK, &ownerChild)
	if !pollUntilSynced(t, 2*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		joinerChild = api.BundleChildResponse{}
		code := tryJSON(t, http.MethodPost, joiner.base+childPath, childBody, &joinerChild)
		return code == http.StatusOK && joinerChild.ObjectId != ""
	}) {
		t.Fatalf("joiner never derived the bundle child: %+v", joinerChild)
	}
	if ownerChild.ObjectId == "" || ownerChild.ObjectId != joinerChild.ObjectId {
		t.Fatalf("child diverged: owner=%q joiner=%q", ownerChild.ObjectId, joinerChild.ObjectId)
	}

	// The joiner writes into the adopted root.
	joinerMd := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + adopted.Bundle.RootId + "/editor/editor_blocks/markdown"
	if res := appendMarkdownE2E(t, joinerMd+"/append", "hello from joiner"); len(res.Inserted) != 1 {
		t.Fatalf("joiner write: %+v", res)
	}

	// Both blocks converge on both peers — one object, one tree.
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		onOwner := getMarkdownContent(t, ownerMd)
		onJoiner := getMarkdownContent(t, joinerMd)
		return strings.Contains(onOwner, "hello from joiner") && strings.Contains(onJoiner, "hello from owner")
	}) {
		t.Fatalf("bundle body did not converge: owner=%q joiner=%q",
			getMarkdownContent(t, ownerMd), getMarkdownContent(t, joinerMd))
	}
}

// appendMarkdownE2E appends a fragment through the append fast path.
func appendMarkdownE2E(t *testing.T, appendURL, content string) markdownSetResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	var resp markdownSetResponse
	mustJSON(t, http.MethodPost, appendURL, string(body), http.StatusOK, &resp)
	return resp
}

// tryJSON issues a request and returns its status instead of failing
// on it — for polling a call that is expected to be refused until the
// space has synced far enough. Any refusal OTHER than the documented
// retryable ones fails the test: polling must not paper over a real
// error, and on this path a wrong answer (a second root) is permanent.
func tryJSON(t *testing.T, method, url, body string, out any) int {
	t.Helper()
	resp, raw := doRequest(t, method, url, body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: decode: %v body=%s", method, url, err, raw)
			}
		}
	case http.StatusConflict:
		var env api.ErrorEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s %s: decode error: %v body=%s", method, url, err, raw)
		}
		switch env.Error.Code {
		case "bundle.not_ready", "bundle.loser_not_ready":
		default:
			t.Fatalf("%s %s: 409 %s is not a retryable state", method, url, env.Error.Code)
		}
	default:
		t.Fatalf("%s %s: status=%d body=%s", method, url, resp.StatusCode, raw)
	}
	return resp.StatusCode
}
