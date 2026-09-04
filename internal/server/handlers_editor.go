package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/editor"
)

// editorCollection resolves the `:collection` segment of the editor
// write routes (`…/editor/:collection/{blocks,markdown}`): the
// collection an editor part declared — the canonical `editor_blocks`
// shared by every type declaring `{"module": "editor", "shared":
// true}`, or a namespaced `<typeId>_<key>` instance. A name the space
// does not serve with the editor module is `404 dataset.not_found`;
// whether THIS object may hold it is the SDK's write-time gate (`400
// dataset.not_declared`). done=true means the response was written.
func (d *deps) editorCollection(c echo.Context, sp space.Space) (collection string, errResp error, done bool) {
	collection = c.Param("collection")
	if collection == "" {
		return "", writeError(c, http.StatusBadRequest, "request.missing_field", "collection required", nil), true
	}
	// The canonical collection is registered on every controller —
	// no catalog read on the common path.
	if collection == editor.Dataset {
		return collection, nil, false
	}
	for _, ds := range sp.Datasets() {
		if ds.Name == collection && ds.Module == editor.Module {
			return collection, nil, false
		}
	}
	return "", writeError(c, http.StatusNotFound, "dataset.not_found",
		"collection is not an editor dataset in this space — the canonical editor_blocks, or a "+
			"namespaced <typeId>_<key> instance a type's part declares with module \"editor\"",
		map[string]any{"spaceId": sp.Id(), "collection": collection}), true
}
