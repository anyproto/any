package server

import (
	"context"
	"errors"
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

// objectIdError is a refusal of the object id a request names. It
// carries the reply objectIdRefused writes.
type objectIdError struct {
	status  int
	code    string
	message string
	details map[string]any
}

func (e *objectIdError) Error() string { return e.message }

// resolveObjectId returns the full id of the object a request names; a
// short id is looked up in the space's objects rows. A refusal is an
// *objectIdError. Call it before any check that compares the id.
func resolveObjectId(ctx context.Context, sp space.Space, id string) (string, error) {
	if len(id) == objectIdLen {
		return id, nil
	}
	// A full id cut short keeps the prefix.
	shortId := strings.TrimPrefix(id, objectIdPrefix)
	// Before the shape check: "undefined" has the shape of a short id.
	if isSerializedNil(shortId) {
		return "", &objectIdError{http.StatusBadRequest, "object.id_required",
			serializedNilIdMessage("objectId", id), map[string]any{"field": "objectId", "got": id}}
	}
	if !isShortId(shortId) {
		return "", &objectIdError{http.StatusBadRequest, "request.invalid_field",
			fmt.Sprintf("objectId must be a full object id (%d characters) or a short id, the head of the id: an optional %q, then %d to %d of the characters that follow it ([a-z2-7])",
				objectIdLen, objectIdPrefix, shortIdMinLen, shortIdMaxLen),
			map[string]any{"field": "objectId", "got": id}}
	}
	matches, err := shortIdMatches(ctx, sp, shortId)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", &objectIdError{http.StatusNotFound, "object.not_found",
			"no object in this space has this short id (unknown or deleted)",
			map[string]any{"spaceId": sp.Id(), "objectId": id}}
	case 1:
		return matches[0].Id, nil
	}
	return "", ambiguousIdError(id, matches)
}

// objectIdRefused writes the reply for an error of resolveObjectId.
func objectIdRefused(c echo.Context, sp space.Space, id string, err error) error {
	var refusal *objectIdError
	if errors.As(err, &refusal) {
		return writeError(c, refusal.status, refusal.code, refusal.message, refusal.details)
	}
	return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": id})
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

// ambiguousIdError refuses a short id that matches several objects.
// matches holds one more than the cap when more exist.
func ambiguousIdError(shortId string, matches []api.ObjectCandidate) *objectIdError {
	count := strconv.Itoa(len(matches))
	if len(matches) > shortIdCandidatesMax {
		matches = matches[:shortIdCandidatesMax]
		count = "more than " + strconv.Itoa(shortIdCandidatesMax)
	}
	return &objectIdError{http.StatusConflict, api.ErrObjectAmbiguousId,
		fmt.Sprintf("short id %q matches %s objects in this space; retry with a longer unambiguous id", shortId, count),
		map[string]any{"shortId": shortId, "candidates": matches}}
}
