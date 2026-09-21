package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/p2p"
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

// @Summary	Direct (p2p) layer snapshot — local network and global (diagnostic, unstable)
// @Tags		debug
// @Produce	json
// @Success	200	{object}	api.P2PStatusResponse
// @Router		/debug/p2p [get]
func (d *deps) debugP2P(c echo.Context) error {
	st := d.sdk.P2PStatus()
	return c.JSON(http.StatusOK, api.P2PStatusResponse{
		PeerId:          st.PeerId,
		Enabled:         st.Enabled,
		ListenerStarted: st.ListenerStarted,
		Port:            st.Port,
		Possibility:     st.Possibility.String(),
		State:           st.State.String(),
		Peers:           p2pPeersToAPI(st.Peers),
		LocalDiscovery:  st.LocalDiscovery,
		Global: api.GlobalP2PStatus{
			Enabled:        st.Global.Enabled,
			EndpointId:     st.Global.EndpointId,
			Ticket:         st.Global.Ticket,
			HomeRelay:      st.Global.HomeRelay,
			RelayConnected: st.Global.RelayConnected,
			Peers:          p2pPeersToAPI(st.Global.Peers),
			Account: api.AccountDiscoveryStatus{
				Enabled:       st.Global.Account.Enabled,
				Relays:        orEmpty(st.Global.Account.Relays),
				Devices:       st.Global.Account.Devices,
				OwnEntry:      st.Global.Account.OwnEntry,
				LastResolved:  nonZeroTime(st.Global.Account.LastResolved),
				LastPublished: nonZeroTime(st.Global.Account.LastPublished),
				LastError:     st.Global.Account.LastError,
				ClockAheadMs:  st.Global.Account.ClockAhead.Milliseconds(),
			},
		},
	})
}

func p2pPeersToAPI(peers []p2p.PeerStatus) []api.P2PPeerStatus {
	out := make([]api.P2PPeerStatus, 0, len(peers))
	for _, p := range peers {
		out = append(out, api.P2PPeerStatus{
			PeerId:    p.PeerId,
			SpaceIds:  orEmpty(p.SpaceIds),
			Connected: p.Connected,
			Sources:   p.Sources,
			LastSeen:  nonZeroTime(p.LastSeen),
			Tier:      p.Tier,
			Failures:  p.Failures,
		})
	}
	return out
}

// nonZeroTime keeps a never-set timestamp off the wire entirely rather
// than sending the zero year, which reads as a real date to a client.
func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// orEmpty keeps a nil slice off the wire as [] rather than null: a
// client iterating the field should not have to special-case the
// absence of peers, heads or relays.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
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
	return api.ObjectDebugResponse{
		ObjectId:        o.ObjectId,
		SyncState:       o.SyncState.String(),
		Pending:         orEmpty(o.Pending),
		LastSyncAt:      o.LastSyncAt,
		Heads:           orEmpty(o.Heads),
		HeadsCount:      o.HeadsCount,
		BranchCount:     o.BranchCount,
		TreeLen:         o.TreeLen,
		Snapshots:       o.Snapshots,
		LatestVersionId: o.LatestVersionId,
		MaxAddSeq:       o.MaxAddSeq,
	}
}
