package httpadapter

import (
	"oneTrick/api"
	"oneTrick/services/stats"
)

func ToSimilarSnapshotsParams(request api.GetSimilarSnapshotsRequestObject) stats.SimilarSnapshotsParams {
	includeStats := true
	if request.Params.IncludeStats != nil {
		includeStats = *request.Params.IncludeStats
	}

	tier := stats.SimilarTierAll
	if request.Params.Tier != nil && *request.Params.Tier == api.SimilarTierFilterHigh {
		tier = stats.SimilarTierHigh
	}

	var gameMode string
	if request.Params.GameMode != nil {
		gameMode = string(*request.Params.GameMode)
	}

	return stats.SimilarSnapshotsParams{
		SnapshotID:   request.SnapshotID,
		IncludeStats: includeStats,
		Tier:         tier,
		GameMode:     gameMode,
	}
}

func ToAPISimilarSnapshotsResponse(
	target api.CharacterSnapshot,
	items []api.SimilarSnapshotItem,
	statsMap map[string]api.PlayerStats,
	countsMap map[string]int,
) api.GetSimilarSnapshots200JSONResponse {
	if items == nil {
		items = []api.SimilarSnapshotItem{}
	}
	var statsPtr *map[string]api.PlayerStats
	if statsMap != nil {
		statsPtr = &statsMap
	}
	var countsPtr *map[string]int
	if countsMap != nil {
		countsPtr = &countsMap
	}
	return api.GetSimilarSnapshots200JSONResponse{
		Target: target,
		Items:  items,
		Stats:  statsPtr,
		Count:  countsPtr,
	}
}
