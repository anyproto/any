//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/stretchr/testify/require"
)

// Offline fixture: these contract checks never require staging.yml, never
// join production and never pass by skipping when a developer lacks a config.
func TestSearch_StructuredHTTP(t *testing.T) {
	d, teardown := newTestDepsCfg(t, func(cfg *config.Config) {
		cfg.Network.Nodeconf = string(config.NodeconfPlaceholder())
	})
	defer teardown()
	e := buildEcho(d)
	ix := newTestIndexer(t, d, nil)
	defer func() { _ = ix.Close() }()
	ctx := context.Background()
	spaceId := mustCreateSpace(t, e, "Structured search")
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types", `{"name":"Wanted","xKey":"wanted"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var typ api.TypesCreateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &typ))
	// All short names rank above the two long matching names. Filtering the
	// old top-100 response would incorrectly return no results for this type.
	for range 105 {
		mustCreateObject(t, e, spaceId, `{"initialProperties":{"any":{"name":"quasar"}}}`)
	}
	wanted := []string{}
	for range 2 {
		wanted = append(wanted, mustCreateObject(t, e, spaceId, `{"types":["`+typ.TypeId+`"],"initialProperties":{"any":{"name":"quasar `+strings.Repeat("padding ", 60)+`"}}}`))
	}
	sp, err := d.sdk.Spaces().Get(ctx, spaceId)
	require.NoError(t, err)
	require.NoError(t, ix.SyncSpace(ctx, sp))
	legacy := doSearch(t, e, spaceId, api.SearchRequest{Query: "quasar", Mode: "fts", Limit: 100}, http.StatusOK)
	for _, h := range legacy.Hits {
		require.NotContains(t, wanted, h.ObjectId, "fixture must place matches beyond legacy cutoff")
	}
	filter := &api.SearchFilter{Kinds: []string{"object"}, TypeIds: []string{typ.TypeId}, Creator: d.account}
	request := api.SearchRequest{Query: "quasar", Filter: filter, Limit: 1}
	first := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, first.Hits, 1)
	require.NotNil(t, first.HasNext)
	require.True(t, *first.HasNext)
	require.Equal(t, "object", first.Hits[0].Kind)
	require.Equal(t, d.account, first.Hits[0].Creator)
	require.Contains(t, wanted, first.Hits[0].ObjectId)
	require.NotNil(t, first.Hits[0].CreatedAt)
	request.Offset = 1
	second := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, second.Hits, 1)
	require.False(t, *second.HasNext)
	require.NotEqual(t, first.Hits[0].ObjectId, second.Hits[0].ObjectId)
	request.Offset = 2
	end := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Empty(t, end.Hits)
	require.False(t, *end.HasNext)
	request.Query, request.Offset, request.Limit, request.Sort = "", 0, 10, "created"
	browse := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, browse.Hits, 2)
	filter.Creator = "somebody-else"
	require.Empty(t, doSearch(t, e, spaceId, request, http.StatusOK).Hits)
	filter.Creator = d.account
	// The SDK object query removes a deleted owner even before the indexer
	// has caught up; text search must not resurrect its stale indexed hit.
	doJSONExpect(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+wanted[0], http.StatusNoContent)
	request.Query = "quasar"
	require.Len(t, doSearch(t, e, spaceId, request, http.StatusOK).Hits, 1)
	// Related + type + creator + text compose before the page cut. The
	// selected page's outgoing link finds its target; reversing selection
	// finds the incoming source. The selected object itself never leaks in.
	source := mustCreateModuleObject(t, e, spaceId, "editor")
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+source+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"quasar [target](any://o/`+spaceId+`/`+wanted[1]+`)"}`, http.StatusCreated)
	require.NoError(t, ix.SyncSpace(ctx, sp))
	filter.RelatedTo = &api.SearchObjectRef{SpaceId: spaceId, ObjectId: source}
	related := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, related.Hits, 1)
	require.Equal(t, wanted[1], related.Hits[0].ObjectId)
	filter.RelatedTo.ObjectId, filter.TypeIds = wanted[1], nil
	related = doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, related.Hits, 1)
	require.Equal(t, source, related.Hits[0].ObjectId)

	chat := mustCreateModuleObject(t, e, spaceId, "chat")
	base := "/v1/spaces/" + spaceId + "/objects/" + chat + "/chat/messages"
	msg := mustModify(t, e, http.MethodPost, base, `{"text":"message body"}`, http.StatusCreated)
	request = api.SearchRequest{Filter: &api.SearchFilter{Kinds: []string{"record"}, Creator: d.account}, Scopes: []string{"chat"}, Sort: "created"}
	messages := doSearch(t, e, spaceId, request, http.StatusOK)
	require.Len(t, messages.Hits, 1, "blank query reads live messages before indexing")
	require.Equal(t, msg.RecordIds[0], messages.Hits[0].RecordId)
	require.Equal(t, "record", messages.Hits[0].Kind)
	require.Equal(t, d.account, messages.Hits[0].Creator)
	require.Equal(t, "message body", messages.Hits[0].Data)

	for _, body := range []string{
		`{"filter":{"kinds":["file"]}}`, `{"filter":{"typeIds":[""]}}`,
		`{"filter":{"relatedTo":{"objectId":"x"}}}`, `{"filter":{},"offset":-1}`,
		`{"filter":{},"sort":"wrong"}`, `{"filter":{},"mode":"vector"}`,
		`{"filter":{},"mode":"hybrid"}`, `{"filter":{},"require":["term"]}`,
		`{"filter":{"typo":1}}`, `{"query":"x","offset":1}`, `{"query":"x","sort":"created"}`,
	} {
		t.Run(body, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search", body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}
