package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/telemetry"
)

func ToTelemetryQuery(request api.GetCharacterTelemetryRequestObject) telemetry.Query {
	q := telemetry.Query{
		UserID:      request.UserID,
		CharacterID: request.CharacterID,
	}
	if request.Params.From != nil {
		q.From = *request.Params.From
	}
	if request.Params.To != nil {
		q.To = *request.Params.To
	}
	if request.Params.GameMode != nil {
		q.GameMode = telemetry.GameMode(*request.Params.GameMode)
	}
	if request.Params.SnapshotID != nil {
		q.SnapshotID = *request.Params.SnapshotID
	}
	if request.Params.Limit != nil {
		q.Limit = *request.Params.Limit
	}
	return q
}

func ToAPITelemetryResponse(res telemetry.Result) api.GetCharacterTelemetry200JSONResponse {
	matches := make([]api.TelemetryMatch, 0, len(res.Matches))
	for _, m := range res.Matches {
		item := api.TelemetryMatch{
			ActivityID: m.ActivityID,
			Period:     m.Period,
			Location:   m.Location,
			Activity:   m.Activity,
			Kills:      m.Kills,
			Deaths:     m.Deaths,
			Assists:    m.Assists,
			Won:        m.Won,
		}
		if m.EstimatedGameMode != "" {
			gm := api.GameMode(m.EstimatedGameMode)
			item.EstimatedGameMode = &gm
		}
		if m.HasTimePlayedSeconds {
			tp := m.TimePlayedSeconds
			item.TimePlayedSeconds = &tp
		}
		if m.SessionID != "" {
			sid := m.SessionID
			item.SessionID = &sid
		}
		if m.SnapshotID != "" {
			snapID := m.SnapshotID
			item.SnapshotID = &snapID
		}
		if m.SnapshotConfidence != "" {
			conf := api.ConfidenceLevel(m.SnapshotConfidence)
			item.SnapshotConfidence = &conf
		}
		matches = append(matches, item)
	}

	snapshots := make(map[string]api.TelemetrySnapshotSummary, len(res.Snapshots))
	for id, summary := range res.Snapshots {
		snapshots[id] = api.TelemetrySnapshotSummary{
			ID:   summary.ID,
			Name: summary.Name,
		}
	}

	return api.GetCharacterTelemetry200JSONResponse{
		Matches:    matches,
		Snapshots:  snapshots,
		TotalCount: res.TotalCount,
		Truncated:  res.Truncated,
	}
}
