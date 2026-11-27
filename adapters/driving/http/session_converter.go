package httpadapter

import (
	"errors"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/session"
)

func ToAPISessionAggregatesResponse(res session.SessionAggregatesResult) api.GetSessionAggregates200JSONResponse {
	aggs := res.Aggregates
	if aggs == nil {
		aggs = []api.Aggregate{}
	}
	snaps := res.Snapshots
	if snaps == nil {
		snaps = map[string]api.CharacterSnapshot{}
	}
	participants := make(map[string]api.SessionParticipant, len(res.Participants))
	for charID, p := range res.Participants {
		participants[charID] = api.SessionParticipant{
			UserID:      p.UserID,
			DisplayName: p.DisplayName,
			CharacterID: p.CharacterID,
			Class:       ptr.NonEmpty(p.CharacterClass),
			EmblemURL:   ptr.NonEmpty(p.EmblemURL),
		}
	}
	return api.GetSessionAggregates200JSONResponse{
		Aggregates:   aggs,
		Snapshots:    snaps,
		Participants: participants,
	}
}

func ToAPISessionAggregatesError(err error) api.GetSessionAggregatesResponseObject {
	if errors.Is(err, session.ErrNotFound) {
		return api.GetSessionAggregates404JSONResponse{
			Message: "session not found",
			Status:  api.ErrUnknownError,
		}
	}
	return api.GetSessionAggregates500JSONResponse{
		Message: err.Error(),
		Status:  api.ErrInternalSystemDown,
	}
}

func ToAPIBackfillSessionSummariesResponse(res session.BackfillSessionSummariesResult) api.BackfillSessionSummaries200JSONResponse {
	return api.BackfillSessionSummaries200JSONResponse{
		DryRun:            res.DryRun,
		AggregatesScanned: int32(res.AggregatesScanned),
		AggregatesUpdated: int32(res.AggregatesUpdated),
		AggregatesFailed:  int32(res.AggregatesFailed),
		SessionsScanned:   int32(res.SessionsScanned),
		SessionsUpdated:   int32(res.SessionsUpdated),
		SessionsFailed:    int32(res.SessionsFailed),
	}
}

func ToAPIBackfillSessionSummariesUnauthorized() api.BackfillSessionSummariesResponseObject {
	return api.BackfillSessionSummaries401JSONResponse{
		Message: "unauthorized",
		Status:  api.ErrUnknownError,
	}
}

func ToAPIBackfillSessionSummariesError(err error) api.BackfillSessionSummariesResponseObject {
	return api.BackfillSessionSummaries500JSONResponse{
		Message: err.Error(),
		Status:  api.ErrInternalSystemDown,
	}
}
