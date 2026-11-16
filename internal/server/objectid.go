package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/anyproto/any-store/v2/query"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// An object id is objectIdPrefix plus 52 base32 characters. A short id
// is the head of the part after the prefix, sent with or without it.
const (
	objectIdPrefix = "bafyrei"
	objectIdLen    = 59
	shortIdMaxLen  = objectIdLen - len(objectIdPrefix)
	// shortIdCandidatesMax caps the matches an ambiguous short id lists.
	shortIdCandidatesMax = 10
)

// shortIdMinLen is the shortest short id accepted. A variable so a test
// can force a collision.
var shortIdMinLen = 5

var objectIdPath = []string{"id"}

// resolveObjectId returns the full id of the object a request names; a
// short id is looked up in the space's objects rows. Call it before any
// check that compares the id. On done the reply is written: return errResp.
func resolveObjectId(c echo.Context, sp space.Space, id string) (fullId string, errResp error, done bool) {
	if len(id) == objectIdLen {
		return id, nil, false
	}
	// First: "undefined" has the shape of a short id.
	if isSerializedNil(id) {
		return "", serializedNilIdError(c, "objectId", id), true
	}
	// A full id cut short keeps the prefix.
	shortId := strings.TrimPrefix(id, objectIdPrefix)
	if !isShortId(shortId) {
		return "", writeError(c, http.StatusBadRequest, "request.invalid_field",
			fmt.Sprintf("objectId must be a full object id (%d characters) or a short id: %d to %d characters of [a-z2-7], the part of the id after %q, with or without that prefix",
				objectIdLen, shortIdMinLen, shortIdMaxLen, objectIdPrefix),
			map[string]any{"field": "objectId", "got": id}), true
	}
	matches, err := shortIdMatches(c.Request().Context(), sp, shortId)
	if err != nil {
		return "", sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": id}), true
	}
	switch len(matches) {
	case 0:
		return "", writeError(c, http.StatusNotFound, "object.not_found",
			"no object in this space has this short id (unknown or deleted)",
			map[string]any{"spaceId": sp.Id(), "objectId": id}), true
	case 1:
		return matches[0].Id, nil, false
	}
	return "", ambiguousIdError(c, id, matches), true
}

// shortIdMatches lists the objects whose id carries the short id, at
// most one more than shortIdCandidatesMax: a primary-key range seek from
// the prefix to its successor ('{' sorts right after 'z').
func shortIdMatches(ctx context.Context, sp space.Space, shortId string) ([]api.ObjectCandidate, error) {
	prefix := objectIdPrefix + shortId
	it, err := sp.QueryObjects().
		Filter(query.And{
			query.Key{Path: objectIdPath, Filter: query.NewComp(query.CompOpGte, prefix)},
			query.Key{Path: objectIdPath, Filter: query.NewComp(query.CompOpLt, prefix+"{")},
		}).
		Limit(shortIdCandidatesMax + 1).
		Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var matches []api.ObjectCandidate
	for it.Next() {
		row, err := it.Doc()
		if err != nil {
			return nil, err
		}
		// Two strings, not the row: a row can be large.
		matches = append(matches, api.ObjectCandidate{
			Id:   row.GetString("id"),
			Name: row.GetString("any", "name"),
		})
	}
	return matches, it.Err()
}

// isShortId reports whether id has the shape of a short id without the
// prefix.
func isShortId(id string) bool {
	if len(id) < shortIdMinLen || len(id) > shortIdMaxLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if ch := id[i]; (ch < 'a' || ch > 'z') && (ch < '2' || ch > '7') {
			return false
		}
	}
	return true
}

// ambiguousIdError answers a short id that matches several objects.
// matches holds one more than the cap when more exist.
func ambiguousIdError(c echo.Context, shortId string, matches []api.ObjectCandidate) error {
	count := strconv.Itoa(len(matches))
	if len(matches) > shortIdCandidatesMax {
		matches = matches[:shortIdCandidatesMax]
		count = "more than " + strconv.Itoa(shortIdCandidatesMax)
	}
	return writeError(c, http.StatusConflict, api.ErrObjectAmbiguousId,
		fmt.Sprintf("short id %q matches %s objects in this space; retry with the full id", shortId, count),
		map[string]any{"shortId": shortId, "candidates": matches})
}
