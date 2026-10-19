package server

import (
	"context"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/page"
)

const bookmarkBundleID = "system:bookmark/v1"

// migrateBookmarkRoot upgrades the unreleased collection prototype in place.
// The root and property IDs never change: values, links, custom columns and
// canonical editor records need no copying. A failed/aborted walk is resumed
// by the next setup; membership is the remaining-work marker. No data is deleted.
// This exception is specific to the prototype, not a general catalog kind heal.
func migrateBookmarkRoot(ctx context.Context, sp space.Space, rootID string, parts []space.PartDraft) error {
	root, err := sp.Objects().Get(ctx, rootID)
	if err != nil {
		return err
	}
	kind := string(root.GetStringBytes("any", "type"))
	if kind != space.CollectionMarker && kind != space.TypeMarker {
		return fmt.Errorf("bookmark definition has unexpected kind %q", kind)
	}
	pending := sp.QueryObjects().Filter(map[string]any{"any.collections": rootID})
	// Do not silently replace a custom type on a page somebody filed as a bookmark.
	conflict, err := sp.QueryObjects().Filter(map[string]any{"$and": []any{map[string]any{"any.collections": rootID}, map[string]any{"any.type": map[string]any{"$nin": []string{page.TypeId, rootID}}}}}).Count(ctx)
	if err != nil {
		return err
	}
	if conflict > 0 {
		return fmt.Errorf("%d bookmarks have another type; migration left them unchanged", conflict)
	}
	count, err := pending.Count(ctx)
	if err != nil {
		return err
	}
	if kind == space.TypeMarker && count == 0 {
		return nil
	}
	switch sp.Info().OwnRole {
	case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
	default:
		return fmt.Errorf("bookmark migration requires a member with write permission")
	}
	if kind == space.CollectionMarker {
		fields := map[string]any{"any.type": space.TypeMarker, "type.xkey": "bookmark"}
		if v := root.Get("collection", "hidden"); v != nil {
			fields["type.hidden"] = v
		}
		if v := root.Get("collection", "meta"); v != nil {
			fields["type.meta"] = v
		}
		if string(root.GetStringBytes("any", "name")) == "Bookmarks" {
			fields["any.name"] = "Bookmark"
		}
		if err := bookmarkModify(ctx, sp, root, []space.Op{{Type: space.OpSet, Value: fields}}); err != nil {
			return err
		}
	}
	// The editor keeps its canonical dataset, so page bodies remain writable.
	existing, err := sp.Types().Parts(ctx, rootID)
	if err != nil {
		return err
	}
	for _, draft := range parts {
		found := false
		for _, part := range existing {
			if part.Key == draft.Key {
				found = true
				break
			}
		}
		if !found {
			if _, err := sp.Types().AddPart(ctx, rootID, draft); err != nil {
				return err
			}
		}
	}
	for {
		rows, err := pending.Limit(100).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			current := string(row.GetStringBytes("any", "type"))
			if current != page.TypeId && current != rootID {
				return fmt.Errorf("bookmark changed type during migration; retry after resolving it")
			}
			if err := bookmarkModify(ctx, sp, row, []space.Op{{Type: space.OpSet, Path: "any.type", Value: rootID}, {Type: space.OpPull, Path: "any.collections", Value: rootID}}); err != nil {
				return err
			}
		}
	}
}

func bookmarkModify(ctx context.Context, sp space.Space, snapshot *anyenc.Value, ops []space.Op) error {
	objectID := string(snapshot.GetStringBytes("id"))
	seq := snapshot.GetInt("_applySeq")
	if seq <= 0 {
		return fmt.Errorf("bookmark migration snapshot has no revision")
	}
	revision := uint64(seq)
	result, err := sp.Modify(ctx, space.ModifyBatch{ObjectId: objectID, IfUnchangedSince: &revision, Dataset: objectsDataset, Records: []space.RecordModify{{Id: objectID, Ops: ops}}})
	if err != nil {
		return err
	}
	if len(result.Rejections) > 0 {
		return fmt.Errorf("bookmark migration write rejected: %v", result.Rejections)
	}
	return nil
}
