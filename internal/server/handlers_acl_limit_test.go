package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any-sync/util/crypto"

	"github.com/anyproto/any/internal/api"
)

func createSpace(t *testing.T, e http.Handler, name string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"name":%q}`, name))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	return sp.Id
}

// expectLimitReached asserts the typed refusal.
func expectLimitReached(t *testing.T, what string, code int, body []byte) {
	t.Helper()
	if code != http.StatusConflict || errEnvCode(t, body) != "acl.limit_reached" {
		t.Fatalf("%s: %d %s, want 409 acl.limit_reached", what, code, body)
	}
}

// TestServer_SharedSpacesLimit shares spaces until the coordinator
// refuses the owner another one: the refusal is 409 acl.limit_reached on
// both ways of sharing the next space.
func TestServer_SharedSpacesLimit(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// More than any test network grants a fresh account.
	const most = 30
	for shared := range most {
		id := createSpace(t, e, fmt.Sprintf("shared-%d", shared))
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+id+"/invites", "")
		if rec.Code == http.StatusCreated {
			continue
		}
		expectLimitReached(t, fmt.Sprintf("invite on space %d", shared), rec.Code, rec.Body.Bytes())
		if shared == 0 {
			t.Fatal("the network refused the first shared space: nothing proves a limit was reached")
		}
		rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+id+"/guest-key", "")
		expectLimitReached(t, "guest key on the same space", rec.Code, rec.Body.Bytes())
		t.Logf("refused after %d shared spaces", shared)
		return
	}
	t.Skipf("the network let one account share %d spaces: no limit to reach", most)
}

// TestServer_MembersLimit adds more writers to one space than a plan
// seats: the coordinator refuses the ACL record, and the refusal is the
// same 409 acl.limit_reached.
func TestServer_MembersLimit(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	id := createSpace(t, e, "crowded")

	// More than any test network seats in one space.
	const writers = 300
	accounts := make([]string, writers)
	for i := range accounts {
		_, pub, err := crypto.GenerateRandomEd25519KeyPair()
		if err != nil {
			t.Fatal(err)
		}
		accounts[i] = fmt.Sprintf(`{"identity":%q,"permission":"writer"}`, pub.Account())
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+id+"/acl/add", `{"accounts":[`+strings.Join(accounts, ",")+`]}`)
	if rec.Code == http.StatusNoContent {
		t.Skipf("the network seated %d writers in one space: no limit to reach", writers)
	}
	expectLimitReached(t, fmt.Sprintf("direct add of %d writers", writers), rec.Code, rec.Body.Bytes())
}

// TestServer_DerivedSpaceUnshareable: a derived space belongs to its
// account alone, and the server says so itself instead of passing on the
// coordinator's plan-limit wording. Needs no network.
func TestServer_DerivedSpaceUnshareable(t *testing.T) {
	d := newUnauthorizedDeps(t)
	network, _ := placeholderNetwork(t)
	d.cfg.Network = network
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("derive bao: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil || sp.Id == "" {
		t.Fatalf("derive bao: %v %s", err, rec.Body.String())
	}
	_, pub, err := crypto.GenerateRandomEd25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for route, body := range map[string]string{
		"/invites":   "",
		"/guest-key": "",
		"/acl/add":   fmt.Sprintf(`{"accounts":[{"identity":%q,"permission":"writer"}]}`, pub.Account()),
	} {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+route, body)
		if rec.Code != http.StatusConflict || errEnvCode(t, rec.Body.Bytes()) != "space.derived_unshareable" {
			t.Errorf("POST %s on a derived space: %d %s, want 409 space.derived_unshareable", route, rec.Code, rec.Body.String())
		}
	}
}
