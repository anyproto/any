package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// deriveBao gives the account a derived bao space. The server opens
// such a space and creates none, so a test derives it through the SDK.
func deriveBao(t testing.TB, d *deps) string {
	t.Helper()
	def, ok := d.derivedSpaceByName("bao")
	if !ok {
		t.Fatal("no bao entry in the derived-space registry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sp, err := d.sdk.Spaces().Derive(ctx, space.DeriveRequest{Seed: []byte(def.Seed), Name: def.Name})
	if err != nil {
		t.Fatalf("derive bao: %v", err)
	}
	if sp.Id() != def.SpaceId {
		t.Fatalf("derived %s, registry resolves %s", sp.Id(), def.SpaceId)
	}
	return sp.Id()
}

func spaceRowOf(t *testing.T, e http.Handler, spaceId string) api.SpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces/%s: %d %s", spaceId, rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func derivedRowOf(t *testing.T, e http.Handler, name string) api.DerivedSpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/derived", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces/derived: %d %s", rec.Code, rec.Body.String())
	}
	var list api.DerivedSpaceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, row := range list.Spaces {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("no %q row in %s", name, rec.Body.String())
	return api.DerivedSpaceInfo{}
}

// A space is created for a use with no network: the reply, the space
// and the list carry it. A space created without one carries none.
func TestSpaces_AnySpaceType(t *testing.T) {
	_, e := offlineDeps(t)
	postAuth(t, e, `{}`)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"Bao","anySpaceType":"bao"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var bao api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &bao); err != nil || bao.AnySpaceType != "bao" || bao.Derived {
		t.Fatalf("create reply: %v %s", err, rec.Body.String())
	}
	if got := spaceRowOf(t, e, bao.Id); got.AnySpaceType != "bao" || got.SpaceType != space.SpaceTypeAny {
		t.Fatalf("space row: %+v", got)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"Plain"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plain: %d %s", rec.Code, rec.Body.String())
	}
	var plain api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil || plain.AnySpaceType != "" {
		t.Fatalf("plain create reply: %v %s", err, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list api.SpaceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	marked := map[string]string{}
	for _, row := range list.Spaces {
		marked[row.Id] = row.AnySpaceType
	}
	if marked[bao.Id] != "bao" || marked[plain.Id] != "" {
		t.Fatalf("list rows: %v", marked)
	}

	// The raw rows carry it too, and a query filters on it.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/query", `{"filter":{"anySpaceType":"bao"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var found api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Id           string `json:"id"`
		AnySpaceType string `json:"anySpaceType"`
	}
	if len(found.Records) != 1 || json.Unmarshal(found.Records[0], &raw) != nil || raw.Id != bao.Id || raw.AnySpaceType != "bao" {
		t.Fatalf("query rows: %s", rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"x","anySpaceType":"`+strings.Repeat("a", 64)+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a 64-character anySpaceType: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"Bao", "bao space", "-bao", "b/ao", strings.Repeat("a", 65)} {
		rec = doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"x","anySpaceType":`+strconv.Quote(bad)+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("anySpaceType %q: %d %s", bad, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "request.invalid_field")
	}
}

// The derived route opens a derived space the account has and creates
// none: an account without one is told so and stays without one.
func TestDerived_OpensNeverCreates(t *testing.T) {
	d, e := offlineDeps(t)
	postAuth(t, e, `{}`)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("derived route with no derived space: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "space.derived_not_created")
	if row := derivedRowOf(t, e, "bao"); row.Created || row.Status != "" {
		t.Fatalf("the refused call left a row: %+v", row)
	}

	baoId := deriveBao(t, d)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	var opened api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &opened); err != nil || opened.Id != baoId || !opened.Derived {
		t.Fatalf("open reply: %v %s", err, rec.Body.String())
	}
	if row := derivedRowOf(t, e, "bao"); !row.Created || row.SpaceId != baoId {
		t.Fatalf("derived row: %+v", row)
	}

	// It stays permanent: the delete is refused and the row survives.
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+baoId, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete of a derived space: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "space.derived_undeletable")
	if got := spaceRowOf(t, e, baoId); got.Status != api.SpaceStatusActive || !got.Derived {
		t.Fatalf("row after the refused delete: %+v", got)
	}
}

// A derived bao space the account holds is not its agent space: its row
// carries no use, also after the account reopens, so a client finds the
// regular space alone.
func TestDerived_NotTheAgentSpace(t *testing.T) {
	d, e := offlineDeps(t)
	created := postAuth(t, e, `{}`)
	baoId := deriveBao(t, d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"Bao","anySpaceType":"bao"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var agent api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &agent); err != nil || agent.Id == "" || agent.Id == baoId {
		t.Fatalf("create reply: %v %s", err, rec.Body.String())
	}

	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	postAuth(t, e, `{"mnemonic":`+strconv.Quote(created.Mnemonic)+`}`)
	waitAuthorized(t, e, created.AccountId)

	if got := spaceRowOf(t, e, baoId); got.AnySpaceType != "" || !got.Derived {
		t.Fatalf("the derived space after the account reopened: %+v", got)
	}
	if got := spaceRowOf(t, e, agent.Id); got.AnySpaceType != "bao" || got.Derived {
		t.Fatalf("the agent space after the account reopened: %+v", got)
	}
}
