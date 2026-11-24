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
