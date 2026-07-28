package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/agentconfig"
	"github.com/anyproto/any/internal/agentsecrets"
	"github.com/anyproto/any/internal/chat"
)

func registerSpaceRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces", d.spaceCreate)
	g.GET("/spaces", d.spaceList)
	// Generic windowed query/subscribe over the account's space list,
	// via Service.Query on the tech-space `spaces` dataset. Registered
	// before the :spaceId routes so the static `query` segment isn't
	// swallowed by the param matcher.
	g.POST("/spaces/query", d.spaceListQuery)
	g.POST("/spaces/query/subscribe", d.spaceListQuerySubscribe)
	g.GET("/spaces/:spaceId", d.spaceGet)
	g.PATCH("/spaces/:spaceId", d.spaceUpdate)
	// Account-private per-space client settings (settings.notifyMode
	// etc.) — separate from the member-replicated PATCH above.
	g.PATCH("/spaces/:spaceId/settings", d.spaceSettingsPatch)
	g.DELETE("/spaces/:spaceId", d.spaceDelete)
	g.POST("/spaces/:spaceId/sync", d.spaceSync)
	g.POST("/spaces/:spaceId/search", d.search)

	g.POST("/spaces/join", d.spaceJoin)
	// Service.Derive / DeriveId are deliberately NOT exposed: a
	// client-supplied seed derives a deterministic space id, so a reused
	// seed re-creates an existing space's identity — too dangerous for
	// clients. Derivation stays an in-process SDK surface.

	// One-to-one (direct) spaces — derived 1-1 shared by two identities.
	// Static `/spaces/one-to-one[...]` segments are registered before the
	// `:spaceId` matcher so they aren't swallowed (and there is no bare
	// POST /spaces/:spaceId to collide with). See docs/03-api.md § Spaces
	// and the SDK's docs/13-one-to-one-spaces.md. Discovery of incoming
	// (pending) requests is via the space list filtered on
	// status=one_to_one_pending — no bespoke endpoint.
	g.POST("/spaces/one-to-one", d.spaceOneToOne)
	g.POST("/spaces/one-to-one/register-incoming", d.spaceOneToOneRegisterIncoming)
	g.POST("/spaces/:spaceId/one-to-one/accept", d.spaceOneToOneAccept)
	g.POST("/spaces/:spaceId/one-to-one/decline", d.spaceOneToOneDecline)

	// Direct-add invites — spaces this account was added to by identity
	// (ACL add). Sender side is POST /v1/spaces/:spaceId/acl/add; incoming
	// invites surface via GET /v1/spaces?status=invite_pending — no
	// bespoke list endpoint, mirroring the 1-1 pattern.
	g.POST("/spaces/:spaceId/invite/accept", d.spaceInviteAccept)
	g.POST("/spaces/:spaceId/invite/decline", d.spaceInviteDecline)

	// Object lifecycle + data plane.
	g.POST("/spaces/:spaceId/objects", d.objectCreate)
	g.POST("/spaces/:spaceId/objects/query", d.spaceQueryObjects)
	g.POST("/spaces/:spaceId/objects/query/subscribe", d.spaceQueryObjectsSubscribe)
	g.POST("/spaces/:spaceId/objects/aggregate", d.spaceAggregateObjects)
	g.DELETE("/spaces/:spaceId/objects/:objectId", d.objectDelete)
	// Reverse reference lookup over links-format property values —
	// consumer-side read, no SDK method behind it (handlers_backlinks.go).
	g.GET("/spaces/:spaceId/objects/:objectId/backlinks", d.objectBacklinks)

	// Editor (built-in type — see internal/editor). Atomic blocks +
	// markdown bridge, both backed by the per-object editor_blocks
	// dataset. Liveness reuses the per-object query/subscribe endpoint
	// with dataset=editor_blocks.
	g.GET("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownGet)
	g.PUT("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownSet)
	g.POST("/spaces/:spaceId/objects/:objectId/editor/markdown/append", d.markdownAppend)
	g.POST("/spaces/:spaceId/objects/:objectId/editor/blocks", d.blocksCreate)
	g.PATCH("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksPatch)
	g.DELETE("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksDelete)

	// Chat (built-in type — see internal/chat). Writes only here;
	// reads + liveness go through /query and /query/subscribe with
	// dataset=chat_messages.
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages", d.chatSend)
	g.PATCH("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatEdit)
	g.DELETE("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatDelete)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji", d.chatReact)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/read-all", d.chatReadAll)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read", d.chatRead)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions-read", d.chatReadReactions)

	// Version history (SDK Space.History(); SDK
	// docs/version-history-proposal.md). Read-only; versions are the
	// ChangeIds every write already returns. The static `diff` segment
	// is registered before the `:version` matcher so it isn't
	// swallowed.
	g.GET("/spaces/:spaceId/objects/:objectId/history", d.historyList)
	g.GET("/spaces/:spaceId/objects/:objectId/history/diff", d.historyDiff)
	g.GET("/spaces/:spaceId/objects/:objectId/history/:version", d.historyViewAt)
	g.GET("/spaces/:spaceId/objects/:objectId/history/:version/datasets/:dataset/records/:recordId", d.historyRecordAt)

	// Agent data layer (built-in types — see internal/agentlog,
	// internal/agentmem and docs/11-agent-memory.md). Writes only here;
	// reads + liveness go through /query and /query/subscribe with
	// dataset ∈ {agent_turns, agent_chunks, agent_memory_items}.
	// Turns/chunks attach to the chat object (multitype chat +
	// agent_log); memory items live on the per-space brain object,
	// which the server resolves itself (GET /agent/brain exposes the
	// deterministic id for reads).
	g.POST("/spaces/:spaceId/objects/:objectId/agent/turns", d.agentTurnAppend)
	g.POST("/spaces/:spaceId/objects/:objectId/agent/chunks", d.agentChunkCreate)

	// Enrichment collection: one sourced record per enrichment fact, written
	// onto a target object by the deterministic apply.
	g.POST("/spaces/:spaceId/objects/:objectId/enriched-data", d.enrichedDataCreate)
	// Deterministic apply of a reviewed enrich_proposal (create/set-property/
	// write enriched_data per item, then delete the proposal). Shared by the
	// UI and agent tooling.
	g.POST("/spaces/:spaceId/enrich/apply", d.enrichApply)
	g.GET("/spaces/:spaceId/agent/brain", d.agentBrainGet)
	g.POST("/spaces/:spaceId/agent/memory", d.agentMemoryCreate)
	g.PATCH("/spaces/:spaceId/agent/memory/:itemId", d.agentMemoryEvolve)
	g.DELETE("/spaces/:spaceId/agent/memory/:itemId", d.agentMemoryDelete)
	g.POST("/spaces/:spaceId/query", d.spaceQuery)
	g.POST("/spaces/:spaceId/query/subscribe", d.spaceQuerySubscribe)
	g.POST("/spaces/:spaceId/aggregate", d.spaceAggregate)
	g.POST("/spaces/:spaceId/modify", d.spaceModify)
	g.POST("/spaces/:spaceId/delete-records", d.spaceDeleteRecords)

	// Dataset schema discovery — JSON Schema (with per-field x-scope) for
	// every dataset the space hosts. See Space.Datasets().
	g.GET("/spaces/:spaceId/datasets", d.spaceDatasets)

	// Types.
	g.GET("/spaces/:spaceId/types", d.typeList)
	g.POST("/spaces/:spaceId/types", d.typeCreate)
	g.GET("/spaces/:spaceId/types/:typeId", d.typeGet)
	g.DELETE("/spaces/:spaceId/types/:typeId", notImplemented("Types.Delete"))
	g.GET("/spaces/:spaceId/types/:typeId/properties", d.typeProperties)
	g.POST("/spaces/:spaceId/types/:typeId/properties", d.typeAddProperty)
	g.DELETE("/spaces/:spaceId/types/:typeId/properties/:propId", d.typeRemoveProperty)
	g.PATCH("/spaces/:spaceId/types/:typeId/properties/:propId", d.typePatchProperty)

	// Properties. Scoped properties (v0.0.11) unified the former
	// base/account/device set endpoints into one scope-aware Set — the
	// patch's propIds determine the scope (all must share one).
	g.GET("/spaces/:spaceId/properties/:objectId", d.propertiesGet)
	g.POST("/spaces/:spaceId/properties/:objectId/set/:typeId", d.propertiesSet)
	g.POST("/spaces/:spaceId/properties/:objectId/attach/:typeId", notImplemented("Properties.AttachType"))
	g.POST("/spaces/:spaceId/properties/:objectId/detach/:typeId", notImplemented("Properties.DetachType"))

	// Members. Static segments before the :identity wildcard so /me,
	// /requests, and /subscribe don't get swallowed by the param matcher.
	g.GET("/spaces/:spaceId/members", d.memberList)
	g.GET("/spaces/:spaceId/members/me", d.memberMe)
	g.GET("/spaces/:spaceId/members/requests", d.memberJoinRequests)
	g.GET("/spaces/:spaceId/members/subscribe", d.subscribeMembers)
	g.GET("/spaces/:spaceId/members/:identity", d.memberGet)

	// Invites — owner/admin side mints + revokes; joiners use the
	// /v1/spaces/join endpoint with the share-friendly token.
	g.POST("/spaces/:spaceId/invites", d.inviteCreate)
	g.GET("/spaces/:spaceId/invites", d.inviteList)
	g.GET("/spaces/:spaceId/invites/:recordId", d.inviteGet)
	g.DELETE("/spaces/:spaceId/invites", d.inviteRevokeAll)
	g.DELETE("/spaces/:spaceId/invites/:recordId", d.inviteRevoke)

	// Guest key — public read-only access. Owner mints/revokes; holders
	// use /v1/spaces/join with the guest token (auto-detected), remove
	// with the regular DELETE /v1/spaces/:spaceId.
	g.POST("/spaces/:spaceId/guest-key", d.guestKeyCreate)
	g.DELETE("/spaces/:spaceId/guest-key", d.guestKeyRevoke)

	// ACL — owner/admin operations on the membership state.
	g.POST("/spaces/:spaceId/acl/accept", d.aclAccept)
	g.POST("/spaces/:spaceId/acl/decline", d.aclDecline)
	g.POST("/spaces/:spaceId/acl/permissions", d.aclChangePermissions)
	g.POST("/spaces/:spaceId/acl/remove", d.aclRemove)
	g.POST("/spaces/:spaceId/acl/add", d.aclAdd)
	g.POST("/spaces/:spaceId/acl/ownership", d.aclOwnership)
	g.POST("/spaces/:spaceId/acl/self-remove", d.aclSelfRemove)
	g.POST("/spaces/:spaceId/acl/cancel-join", d.aclCancelJoin)
	g.POST("/spaces/:spaceId/acl/stop-sharing", d.aclStopSharing)

	// Files (files v2 — see internal/server/handlers_files.go and
	// docs/17-files.md). Attach streams the raw request body (exempt
	// from the global BodyLimit — see routes.go); /content serves raw
	// bytes with Range support. Static segments (stats, subscribe)
	// before the :fileId matcher so they aren't swallowed. Payload-row
	// reads go through the per-object files/query[/subscribe] bridge —
	// the payloads dataset lives on a derived child object the generic
	// /query can't reach.
	g.POST("/spaces/:spaceId/objects/:objectId/files", d.fileAttach)
	g.POST("/spaces/:spaceId/objects/:objectId/files/query", d.filesQuery)
	g.POST("/spaces/:spaceId/objects/:objectId/files/query/subscribe", d.filesQuerySubscribe)
	g.GET("/spaces/:spaceId/files", d.fileList)
	g.GET("/spaces/:spaceId/files/stats", d.fileStats)
	g.GET("/spaces/:spaceId/files/subscribe", d.fileSubscribe)
	g.GET("/spaces/:spaceId/files/:fileId", d.fileGet)
	g.GET("/spaces/:spaceId/files/:fileId/content", d.fileContent)
	g.GET("/spaces/:spaceId/files/:fileId/status", d.fileStatusGet)
	g.POST("/spaces/:spaceId/files/:fileId/pin", d.filePin)
	g.POST("/spaces/:spaceId/files/:fileId/retry", d.fileRetry)
	g.POST("/spaces/:spaceId/files/:fileId/offload", d.fileOffload)
	g.DELETE("/spaces/:spaceId/files/:fileId", d.fileDelete)

	// Sync status — per-space rollup + per-object state. The peers
	// row stays 501 until the SDK exposes a stable per-space peer
	// list on SyncStatusAPI (DebugAPI.Space surfaces the diagnostic
	// equivalent today, see /debug below).
	g.GET("/spaces/:spaceId/sync-status", d.syncStatusSpaceGet)
	g.GET("/spaces/:spaceId/sync-status/objects/:objectId", d.syncStatusObjectGet)
	g.GET("/spaces/:spaceId/sync-status/objects/:objectId/subscribe", d.syncStatusObjectSubscribe)
	g.GET("/spaces/:spaceId/sync-status/peers", notImplemented("SyncStatus.Peers"))

	// Debug — diagnostic surface; not a stable interface. Production UI
	// should use /sync-status (above) once the SDK lands the
	// production-grade methods. See internal/api/debug.go and
	// any-sync-sdk/space/debug.go for the field-level docs.
	g.GET("/spaces/:spaceId/debug", d.debugSpace)
	g.GET("/spaces/:spaceId/debug/objects/:objectId", d.debugObject)
}

// @Summary	Create a space
// @Tags		spaces
// @Accept		json
// @Produce	json
// @Param		body	body		api.SpaceCreateRequest	true	"Space params"
// @Success	201		{object}	api.SpaceInfo
// @Failure	400		{object}	api.ErrorEnvelope
// @Failure	500		{object}	api.ErrorEnvelope
// @Router		/spaces [post]
func (d *deps) spaceCreate(c echo.Context) error {
	req, ok := bindBody[api.SpaceCreateRequest](c)
	if !ok {
		return nil
	}
	sp, err := d.sdk.Spaces().Create(c.Request().Context(), space.CreateRequest{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		SpaceType:   req.SpaceType,
	})
	if err != nil {
		return spaceError(c, err, "")
	}
	return c.JSON(http.StatusCreated, spaceToAPI(c.Request().Context(), sp))
}

// @Summary	List spaces
// @Tags		spaces
// @Produce	json
// @Param		status	query		string	false	"Filter by status; defaults to active-only. Pass 'all' to include deleted/dead rows."
// @Success	200	{object}	api.SpaceListResponse
// @Failure	500	{object}	api.ErrorEnvelope
// @Router		/spaces [get]
func (d *deps) spaceList(c echo.Context) error {
	ctx := c.Request().Context()
	infos, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return spaceError(c, err, "")
	}
	// TEMPORARY WORKAROUND: default the list to active spaces only.
	// Soft-deleted spaces are never offloaded yet (no proper space
	// deletion / offloading — see docs/07-roadmap.md), so the raw list
	// accumulates dozens of dead rows that swamp the real ones. Until
	// that lands, hide non-active rows by default; `?status=all` (or an
	// explicit status string) opts back into the full list. Remove this
	// filter once deletion actually reclaims the rows.
	statusFilter := c.QueryParam("status")
	if statusFilter == "" {
		statusFilter = api.SpaceStatusActive
	}
	out := make([]api.SpaceInfo, 0, len(infos))
	for _, info := range infos {
		if statusFilter != "all" && spaceStatusString(info.Status) != statusFilter {
			continue
		}
		row := spaceInfoToAPI(info)
		// Resolve the deterministic spaceIndex object id for ACTIVE rows
		// only. Service.Get materializes the space, so probing every row
		// would download not-yet-accepted direct-add invites (defeating
		// the SDK's accept gate) and pointlessly load joining/tombstoned
		// rows; non-active rows just omit the field.
		if info.Status == space.StatusActive {
			if sp, err := d.sdk.Spaces().Get(ctx, info.Id); err == nil {
				row.SpaceIndexObjectId = sp.SpaceIndexObjectId()
			}
		}
		out = append(out, row)
	}
	return c.JSON(http.StatusOK, api.SpaceListResponse{Spaces: out})
}

// @Summary	Get a space
// @Tags		spaces
// @Produce	json
// @Param		spaceId	path		string	true	"Space ID"
// @Success	200		{object}	api.SpaceInfo
// @Failure	500		{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId} [get]
func (d *deps) spaceGet(c echo.Context) error {
	id := c.Param("spaceId")
	ctx := c.Request().Context()
	// Row status first, Get only for active rows — same care the list
	// handler takes. Service.Get materializes the space (a missing
	// local storage triggers any-sync's SpacePull bootstrap), so
	// calling it on a pending row would download a not-yet-accepted
	// join / 1-1 / direct-add invite, or pointlessly load a tombstone.
	// Those rows serve the index row info instead. Unknown (empty
	// statuses — legacy rows with no info) loads like active, matching
	// the SDK's own materialization guard.
	infos, lErr := d.sdk.Spaces().List(ctx)
	if lErr == nil {
		for _, info := range infos {
			if info.Id == id && info.Status != space.StatusActive && info.Status != space.StatusUnknown {
				return c.JSON(http.StatusOK, spaceInfoToAPI(info))
			}
		}
	}
	sp, err := d.sdk.Spaces().Get(ctx, id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceToAPI(ctx, sp))
}

// spaceUpdate handles PATCH /v1/spaces/:spaceId.
//
//	@Summary	Update space metadata
//	@Tags		spaces
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		body	body	api.SpaceUpdateRequest	true	"At least one field required"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId} [patch]
func (d *deps) spaceUpdate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	req, ok := bindBody[api.SpaceUpdateRequest](c)
	if !ok {
		return nil
	}
	if req.Name == nil && req.Description == nil && req.IconCID == nil {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description, iconCid is required", nil)
	}
	if err := sp.SetMetadata(c.Request().Context(), space.SetMetadataRequest{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
	}); err != nil {
		return spaceError(c, err, sp.Id())
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceSync forces an immediate head-sync (diff) round against the
// space's responsible nodes instead of waiting for the periodic
// headsync timer. Blocks until the round completes. Normal operation
// never needs this — periodic + reactive sync keep the space current —
// it exists for on-demand convergence ("sync now", tests).
//
//	@Summary	Force an immediate head-sync round (sync now)
//	@Tags		spaces
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/sync [post]
func (d *deps) spaceSync(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.SyncHeads(c.Request().Context()); err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary	Delete a space
// @Tags		spaces
// @Param		spaceId	path	string	true	"Space ID"
// @Success	204
// @Failure	500	{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId} [delete]
func (d *deps) spaceDelete(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().Delete(c.Request().Context(), id); err != nil {
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceOneToOne handles POST /v1/spaces/one-to-one — Service.OneToOne.
// Derives the 1-1 (direct) space shared with the given account identity
// and activates it locally (implicit self-approval → status active).
// Same id regardless of key order; idempotent; overrides a prior local
// decline (un-decline).
//
//	@Summary	Open a 1-1 (direct) space
//	@Tags		spaces
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.SpaceOneToOneRequest	true	"Peer account identity"
//	@Success	201		{object}	api.SpaceInfo
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/one-to-one [post]
func (d *deps) spaceOneToOne(c echo.Context) error {
	req, ok := bindBody[api.SpaceOneToOneRequest](c)
	if !ok {
		return nil
	}
	if req.OtherIdentity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "otherIdentity required", nil)
	}
	sp, err := d.sdk.Spaces().OneToOne(c.Request().Context(), req.OtherIdentity)
	if err != nil {
		return oneToOneError(c, err, "otherIdentity")
	}
	return c.JSON(http.StatusCreated, spaceToAPI(c.Request().Context(), sp))
}

// spaceOneToOneAccept handles POST /v1/spaces/:spaceId/one-to-one/accept
// — Service.AcceptOneToOne. Approves an incoming pending 1-1 by space id
// (the peer identity is read off the row), materializing and activating
// it. Equivalent to re-running OneToOne(peer); idempotent.
//
//	@Summary	Accept an incoming 1-1 (direct) space
//	@Tags		spaces
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID (from a one_to_one_pending row)"
//	@Success	200		{object}	api.SpaceInfo
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/one-to-one/accept [post]
func (d *deps) spaceOneToOneAccept(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().AcceptOneToOne(c.Request().Context(), id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceToAPI(c.Request().Context(), sp))
}

// spaceOneToOneDecline handles POST /v1/spaces/:spaceId/one-to-one/decline
// — Service.DeclineOneToOne. Rejects an incoming pending 1-1, writing a
// synced sticky marker so it is suppressed on every device and never
// auto-resurfaces. A later explicit POST /v1/spaces/one-to-one overrides
// it.
//
//	@Summary	Decline an incoming 1-1 (direct) space
//	@Tags		spaces
//	@Param		spaceId	path	string	true	"Space ID (from a one_to_one_pending row)"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/one-to-one/decline [post]
func (d *deps) spaceOneToOneDecline(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().DeclineOneToOne(c.Request().Context(), id); err != nil {
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceOneToOneRegisterIncoming handles
// POST /v1/spaces/one-to-one/register-incoming — Service.RegisterIncoming.
// The out-of-band discovery path: record an incoming 1-1 request learned
// through the app's own channel (QR, link, member list) as a device-local
// one_to_one_pending row for the user to approve, without materializing
// storage. No-op if a row for the derived space already exists.
//
//	@Summary	Register an out-of-band incoming 1-1 request
//	@Tags		spaces
//	@Accept		json
//	@Param		body	body	api.SpaceRegisterIncomingRequest	true	"Peer identity + optional display hint"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/one-to-one/register-incoming [post]
func (d *deps) spaceOneToOneRegisterIncoming(c echo.Context) error {
	req, ok := bindBody[api.SpaceRegisterIncomingRequest](c)
	if !ok {
		return nil
	}
	if req.PeerIdentity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "peerIdentity required", nil)
	}
	err := d.sdk.Spaces().RegisterIncoming(c.Request().Context(), req.PeerIdentity, space.AccountMetadata{
		Name:        req.DisplayHint.Name,
		Description: req.DisplayHint.Description,
		IconCID:     req.DisplayHint.IconCID,
	})
	if err != nil {
		return oneToOneError(c, err, "peerIdentity")
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceInviteAccept handles POST /v1/spaces/:spaceId/invite/accept —
// Service.AcceptInvite. Approves a direct-add invite: the account is
// already an ACL member, so accept flips the synced status to active
// (every device converges) and loads the space. 200 with the loaded
// space, or 202 when the content isn't pullable yet — loading continues
// durably in the background; poll GET /v1/spaces/:spaceId for the flip.
// Idempotent; also overrides a prior decline.
//
//	@Summary	Accept a direct-add invite
//	@Tags		spaces
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID (from an invite_pending row)"
//	@Success	200		{object}	api.SpaceInfo	"Accepted and loaded"
//	@Success	202		{object}	api.SpaceInfo	"Accepted; load continues in background"
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invite/accept [post]
func (d *deps) spaceInviteAccept(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().AcceptInvite(c.Request().Context(), id)
	if err == nil {
		return c.JSON(http.StatusOK, spaceToAPI(c.Request().Context(), sp))
	}
	// spaceimpl.ErrInviteAcceptPending is internal-only; match the
	// documented error string (same pragmatic pattern as spaceJoin).
	if strings.Contains(err.Error(), "invite accepted; space load pending") {
		infos, lErr := d.sdk.Spaces().List(c.Request().Context())
		if lErr == nil {
			for _, info := range infos {
				if info.Id == id {
					return c.JSON(http.StatusAccepted, spaceInfoToAPI(info))
				}
			}
		}
		return c.JSON(http.StatusAccepted, api.SpaceInfo{
			Id:     id,
			Status: api.SpaceStatusActive,
		})
	}
	return inviteStateError(c, err, id)
}

// spaceInviteDecline handles POST /v1/spaces/:spaceId/invite/decline —
// Service.DeclineInvite. Rejects a direct-add invite: a synced sticky
// marker suppresses it on every device; a later accept overrides it. No
// ACL change happens — the account stays a member on the space's ACL.
//
//	@Summary	Decline a direct-add invite
//	@Tags		spaces
//	@Param		spaceId	path	string	true	"Space ID (from an invite_pending row)"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invite/decline [post]
func (d *deps) spaceInviteDecline(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().DeclineInvite(c.Request().Context(), id); err != nil {
		return inviteStateError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// inviteStateError maps the AcceptInvite / DeclineInvite family of SDK
// errors to the canonical envelope.
//
// STOPGAP: matched on message text until the SDK exports errors.Is-able
// sentinels for this family.
func inviteStateError(c echo.Context, err error, spaceID string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown space"):
		return writeError(c, http.StatusNotFound, "space.not_found",
			"space not found", map[string]any{"spaceId": spaceID})
	case strings.Contains(msg, "is a 1-1 space"):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"this is a 1-1 space — use the one-to-one accept/decline endpoints",
			map[string]any{"spaceId": spaceID})
	case strings.Contains(msg, "not invite-pending"):
		return writeError(c, http.StatusConflict, "space.not_invite_pending",
			"space is not awaiting invite approval", map[string]any{"spaceId": spaceID})
	case strings.Contains(msg, "is deleted"):
		return writeError(c, http.StatusConflict, "space.deleted",
			"space is deleted", map[string]any{"spaceId": spaceID})
	default:
		return spaceError(c, err, spaceID)
	}
}

// oneToOneError maps the OneToOne / RegisterIncoming family of SDK errors
// to the canonical envelope.
//
// STOPGAP: matched on message text. The SDK rejects self-pairing and
// undecodable identities but doesn't yet export errors.Is-able sentinels
// for them (same pragmatic pattern spaceJoin uses for "join pending").
// Grow this into an errors.Is map once the SDK exports the sentinels.
// `field` is the request field the identity came from (otherIdentity /
// peerIdentity), echoed in details.
func oneToOneError(c echo.Context, err error, field string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "cannot pair with self"):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"cannot open a 1-1 space with your own identity",
			map[string]any{"field": field, "reason": "self"})
	case strings.Contains(msg, "decode identity"):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid account identity",
			map[string]any{"field": field})
	default:
		return spaceError(c, err, "")
	}
}

// spaceError maps SDK-side errors to the canonical envelope. It is
// deliberately conservative: the SDK's error sentinels for "unknown
// space" aren't yet exported, so we recognize context errors and fall
// back to 500 + internal for everything else. As the SDK grows
// errors.Is-able sentinels we should grow this map (e.g. ErrSpaceNotFound
// → 404 space.not_found, ErrSpaceExists → 409 space.exists).
func spaceError(c echo.Context, err error, spaceID string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	if errors.Is(err, space.ErrSpaceNotAccepted) {
		var details map[string]any
		if spaceID != "" {
			details = map[string]any{"spaceId": spaceID}
		}
		return writeError(c, http.StatusConflict, "space.not_accepted",
			"space join is pending approval; not materialized on this device", details)
	}
	if errors.Is(err, space.ErrSpaceDeleted) {
		var details map[string]any
		if spaceID != "" {
			details = map[string]any{"spaceId": spaceID}
		}
		return writeError(c, http.StatusConflict, "space.deleted",
			"space is deleted", details)
	}
	details := map[string]any{}
	if spaceID != "" {
		details["spaceId"] = spaceID
	}
	if len(details) == 0 {
		details = nil
	}
	handlerLog.Error("unclassified space error", zap.Error(err))
	return writeError(c, http.StatusInternalServerError, "internal", "internal error", details)
}

func spaceInfoToAPI(info space.SpaceInfo) api.SpaceInfo {
	out := api.SpaceInfo{
		Id:          info.Id,
		Type:        info.Type,
		SpaceType:   info.SpaceType,
		Author:      info.Author,
		Name:        info.Name,
		Description: info.Description,
		IconCID:     info.IconCID,
		Status:      spaceStatusString(info.Status),
		OwnRole:     spacePermissionString(info.OwnRole),
		CreatedAt:   info.CreatedAt,
		Settings:    info.Settings,
	}
	if pk := info.PushKeys; pk != nil {
		out.Push = &api.SpacePushKeys{
			SpaceKey: pk.SpaceKey,
			EncKey:   pk.EncKey,
			EncKeyId: pk.EncKeyId,
		}
	}
	return out
}

// spaceToAPI is the Space-handle variant of spaceInfoToAPI — populates
// SpaceIndexObjectId from the resident space handle, plus
// GeneralChatObjectId by deriving (materializing on first sight) the
// space's single general chat, so single-space responses always carry
// both. This is the one place every single-space path (create / get /
// one-to-one / join) funnels through, so it's where "every space has a
// chat" is enforced. Derive is best-effort: on failure the field is
// omitted rather than failing the whole response (mirrors the list
// path's tolerance for a missing SpaceIndexObjectId).
func spaceToAPI(ctx context.Context, sp space.Space) api.SpaceInfo {
	out := spaceInfoToAPI(sp.Info())
	out.SpaceIndexObjectId = sp.SpaceIndexObjectId()
	if id, err := chat.DeriveGeneralChatObjectId(ctx, sp); err == nil {
		out.GeneralChatObjectId = id
	}
	if id, err := agentconfig.DeriveConfigObjectId(ctx, sp); err == nil {
		out.AgentConfigObjectId = id
	}
	if id, err := agentsecrets.DeriveSecretsObjectId(ctx, sp); err == nil {
		out.AgentSecretsObjectId = id
	}
	return out
}

func spaceStatusString(s space.Status) string {
	switch s {
	case space.StatusActive:
		return api.SpaceStatusActive
	case space.StatusJoining:
		return api.SpaceStatusJoining
	case space.StatusLeaving:
		return api.SpaceStatusLeaving
	case space.StatusDeleted:
		return api.SpaceStatusDeleted
	case space.StatusRemoteDead:
		return api.SpaceStatusRemoteDead
	case space.StatusOneToOnePending:
		return api.SpaceStatusOneToOnePending
	case space.StatusOneToOneDeclined:
		return api.SpaceStatusOneToOneDeclined
	case space.StatusInvitePending:
		return api.SpaceStatusInvitePending
	case space.StatusInviteDeclined:
		return api.SpaceStatusInviteDeclined
	case space.StatusGuestRevoked:
		return api.SpaceStatusGuestRevoked
	default:
		return api.SpaceStatusUnknown
	}
}

func spacePermissionString(p space.Permission) string {
	switch p {
	case space.PermissionReader:
		return api.SpacePermissionReader
	case space.PermissionGuest:
		return api.SpacePermissionGuest
	case space.PermissionWriter:
		return api.SpacePermissionWriter
	case space.PermissionAdmin:
		return api.SpacePermissionAdmin
	case space.PermissionOwner:
		return api.SpacePermissionOwner
	default:
		return api.SpacePermissionNone
	}
}
