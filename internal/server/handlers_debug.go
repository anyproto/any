package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// @Summary	Debug space sync info (diagnostic, unstable)
// @Tags		debug
// @Produce	json
// @Param		spaceId	path		string	true	"Space ID"
// @Success	200		{object}	api.SpaceDebugResponse
// @Failure	500		{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId}/debug [get]
func (d *deps) debugSpace(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	snap := sp.Debug().Space()
	return c.JSON(http.StatusOK, spaceDebugToAPI(snap))
}

// @Summary	Debug object tree info (diagnostic, unstable)
// @Tags		debug
// @Produce	json
// @Param		spaceId		path		string	true	"Space ID"
// @Param		objectId	path		string	true	"Object ID"
// @Success	200			{object}	api.ObjectDebugResponse
// @Failure	400			{object}	api.ErrorEnvelope
// @Failure	500			{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId}/debug/objects/{objectId} [get]
func (d *deps) debugObject(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	snap, err := sp.Debug().Object(c.Request().Context(), objectId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"objectId": objectId})
	}
	return c.JSON(http.StatusOK, objectDebugToAPI(snap))
}

// @Summary	Local-network (p2p) layer snapshot (diagnostic, unstable)
// @Tags		debug
// @Produce	json
// @Success	200	{object}	api.P2PStatusResponse
// @Router		/debug/p2p [get]
func (d *deps) debugP2P(c echo.Context) error {
	st := d.sdk.P2PStatus()
	peers := make([]api.P2PPeerStatus, 0, len(st.Peers))
	for _, p := range st.Peers {
		spaceIds := p.SpaceIds
		if spaceIds == nil {
			spaceIds = []string{}
		}
		peers = append(peers, api.P2PPeerStatus{
			PeerId:    p.PeerId,
			SpaceIds:  spaceIds,
			Connected: p.Connected,
		})
	}
	return c.JSON(http.StatusOK, api.P2PStatusResponse{
		PeerId:          st.PeerId,
		Enabled:         st.Enabled,
		ListenerStarted: st.ListenerStarted,
		Port:            st.Port,
		Possibility:     st.Possibility.String(),
		State:           st.State.String(),
		Peers:           peers,
	})
}

func spaceDebugToAPI(s space.SpaceDebug) api.SpaceDebugResponse {
	peers := make([]api.PeerSyncStats, 0, len(s.Peers))
	for _, p := range s.Peers {
		peers = append(peers, api.PeerSyncStats{
			PeerId:     p.PeerId,
			LastSyncAt: p.LastSyncAt,
			New:        p.New,
			Changed:    p.Changed,
			LastErr:    p.LastErr,
		})
	}
	return api.SpaceDebugResponse{SpaceId: s.SpaceId, Peers: peers}
}

func objectDebugToAPI(o space.ObjectDebug) api.ObjectDebugResponse {
	heads := o.Heads
	if heads == nil {
		heads = []string{}
	}
	pending := o.Pending
	if pending == nil {
		pending = []string{}
	}
	return api.ObjectDebugResponse{
		ObjectId:        o.ObjectId,
		SyncState:       o.SyncState.String(),
		Pending:         pending,
		LastSyncAt:      o.LastSyncAt,
		Heads:           heads,
		HeadsCount:      o.HeadsCount,
		BranchCount:     o.BranchCount,
		TreeLen:         o.TreeLen,
		Snapshots:       o.Snapshots,
		LatestVersionId: o.LatestVersionId,
		MaxAddSeq:       o.MaxAddSeq,
	}
}
