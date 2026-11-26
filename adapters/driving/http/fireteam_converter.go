package httpadapter

import (
	"errors"
	"net/http"
	"oneTrick/api"
	"oneTrick/core/fireteam"
	"oneTrick/ptr"
)

func ToAPIFireteamMembers(members []fireteam.Member) []api.FireteamMember {
	out := make([]api.FireteamMember, 0, len(members))
	for _, m := range members {
		chars := make([]api.Character, 0, len(m.Characters))
		for _, c := range m.Characters {
			chars = append(chars, ToAPICharacter(c))
		}
		active := make([]api.FireteamActiveSession, 0, len(m.ActiveSessions))
		for _, ses := range m.ActiveSessions {
			active = append(active, api.FireteamActiveSession{
				SessionID:   ses.SessionID,
				CharacterID: ses.CharacterID,
				StartedAt:   ses.StartedAt,
				TrackComp:   new(ses.TrackComp),
			})
		}
		out = append(out, api.FireteamMember{
			ID:             m.ID,
			MembershipID:   m.MembershipID,
			DisplayName:    m.DisplayName,
			Characters:     chars,
			ActiveSessions: new(active),
		})
	}
	return out
}

func ToAPICharacter(c fireteam.Character) api.Character {
	var statsPtr *map[string]api.ClassStat
	if c.Stats != nil {
		stats := make(map[string]api.ClassStat, len(c.Stats))
		for k, st := range c.Stats {
			stats[k] = api.ClassStat{
				Name:            st.Name,
				Description:     st.Description,
				Icon:            st.Icon,
				HasIcon:         st.HasIcon,
				StatCategory:    st.StatCategory,
				AggregationType: st.AggregationType,
				Value:           st.Value,
			}
		}
		statsPtr = &stats
	}

	return api.Character{
		Id:                  c.ID,
		Class:               c.Class,
		Race:                c.Race,
		CurrentTitle:        c.CurrentTitle,
		Light:               c.Light,
		EmblemURL:           c.EmblemURL,
		EmblemBackgroundURL: c.EmblemBackgroundURL,
		EmblemColor: api.Color{
			Red:   c.EmblemColor.Red,
			Green: c.EmblemColor.Green,
			Blue:  c.EmblemColor.Blue,
			Alpha: c.EmblemColor.Alpha,
		},
		Stats: statsPtr,
	}
}

func ToStartSessionTargets(members []api.FireteamSessionMemberTarget) []fireteam.StartSessionTarget {
	out := make([]fireteam.StartSessionTarget, 0, len(members))
	for _, m := range members {
		out = append(out, fireteam.StartSessionTarget{
			UserID:      m.UserID,
			CharacterID: m.CharacterID,
		})
	}
	return out
}

func ToAPIStartFireteamSessionsResponse(results []fireteam.StartSessionResult) api.StartFireteamSessions200JSONResponse {
	apiResults := make([]api.FireteamSessionResult, 0, len(results))
	for _, r := range results {
		outcome := api.FireteamSessionOutcomeStarted
		if r.Outcome == fireteam.StartSessionOutcomeSkippedActive {
			outcome = api.FireteamSessionOutcomeSkippedActive
		}
		apiResults = append(apiResults, api.FireteamSessionResult{
			UserID:      r.UserID,
			CharacterID: r.CharacterID,
			Outcome:     outcome,
			Session:     ToAPISession(r.Session),
		})
	}
	return api.StartFireteamSessions200JSONResponse{
		Results: apiResults,
	}
}

func ToAPISession(ses fireteam.Session) api.Session {
	aggs := ses.AggregateIDs
	if aggs == nil {
		aggs = make([]string, 0)
	}
	out := api.Session{
		ID:                 ses.ID,
		UserID:             ses.UserID,
		CharacterID:        ses.CharacterID,
		Name:               ptr.NonEmpty(ses.Name),
		Description:        ptr.NonEmpty(ses.Description),
		StartedAt:          ses.StartedAt,
		CompletedAt:        ses.CompletedAt,
		StartedBy:          toAPIAuditField(ses.StartedBy),
		CompletedBy:        toAPIAuditField(ses.CompletedBy),
		AggregateIDs:       aggs,
		LastSeenActivityID: ptr.NonEmpty(ses.LastSeenActivityID),
		LastSeenTimestamp:  ses.LastSeenTimestamp,
		CompTracking:       toAPICompTracking(ses.CompTracking),
		Summary:            toAPISessionSummary(ses.Summary),
	}
	if ses.Status != "" {
		status := api.SessionStatus(ses.Status)
		out.Status = &status
	}
	return out
}

func toAPIAuditField(a *fireteam.AuditField) *api.AuditField {
	if a == nil {
		return nil
	}
	return &api.AuditField{
		ID:       a.ID,
		Username: a.Username,
	}
}

func toAPICompTracking(ct *fireteam.CompTracking) *api.CompTracking {
	if ct == nil {
		return nil
	}
	return &api.CompTracking{
		Enabled:    ct.Enabled,
		Start:      toAPICompRankSnapshot(ct.Start),
		End:        toAPICompRankSnapshot(ct.End),
		PointDelta: ct.PointDelta,
	}
}

func toAPICompRankSnapshot(snap *fireteam.CompRankSnapshot) *api.CompRankSnapshot {
	if snap == nil {
		return nil
	}
	return &api.CompRankSnapshot{
		Points:       snap.Points,
		StepIndex:    snap.StepIndex,
		DivisionName: ptr.NonEmpty(snap.DivisionName),
		Icon:         ptr.NonEmpty(snap.Icon),
		RecordedAt:   snap.RecordedAt,
	}
}

func toAPISessionSummary(sum *fireteam.SessionSummary) *api.SessionSummary {
	if sum == nil {
		return nil
	}
	modes := sum.ModesPlayed
	if modes == nil {
		modes = make([]string, 0)
	}
	weapons := make([]api.SessionWeaponSummary, 0, len(sum.TopWeapons))
	for _, w := range sum.TopWeapons {
		weapons = append(weapons, api.SessionWeaponSummary{
			Name:  ptr.NonEmpty(w.Name),
			Icon:  ptr.NonEmpty(w.Icon),
			Kills: new(w.Kills),
		})
	}
	companions := make([]api.SessionCompanion, 0, len(sum.Companions))
	for _, c := range sum.Companions {
		sessions := make([]api.SessionCompanionSession, 0, len(c.Sessions))
		for _, cs := range c.Sessions {
			sessions = append(sessions, api.SessionCompanionSession{
				SessionID:     cs.SessionID,
				CharacterID:   cs.CharacterID,
				Class:         ptr.NonEmpty(cs.Class),
				EmblemURL:     ptr.NonEmpty(cs.EmblemURL),
				SharedMatches: cs.SharedMatches,
			})
		}
		companions = append(companions, api.SessionCompanion{
			UserID:        c.UserID,
			DisplayName:   c.DisplayName,
			SharedMatches: c.SharedMatches,
			Wins:          c.Wins,
			Losses:        c.Losses,
			Sessions:      sessions,
		})
	}
	return &api.SessionSummary{
		TotalMatches: new(sum.TotalMatches),
		Wins:         new(sum.Wins),
		Losses:       new(sum.Losses),
		Kills:        new(sum.Kills),
		Deaths:       new(sum.Deaths),
		Assists:      new(sum.Assists),
		WinRate:      new(sum.WinRate),
		KDRatio:      new(sum.KDRatio),
		KDARatio:     new(sum.KDARatio),
		ModesPlayed:  &modes,
		TopWeapons:   &weapons,
		Companions:   &companions,
	}
}

func classifyFireteamSessionError(err error) (int, api.OneTrickError) {
	switch {
	case errors.Is(err, fireteam.ErrInvalidRequest), errors.Is(err, fireteam.ErrAlreadyActive):
		return http.StatusBadRequest, api.OneTrickError{
			Message: err.Error(),
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, fireteam.ErrUnauthorized):
		return http.StatusUnauthorized, api.OneTrickError{
			Message: "unauthorized",
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, fireteam.ErrNotInFireteam):
		return http.StatusForbidden, api.OneTrickError{
			Message: "target user is not in your current fireteam",
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, fireteam.ErrUserNotFound):
		return http.StatusNotFound, api.OneTrickError{
			Message: "user not found",
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, fireteam.ErrDestinyUnavailable):
		return http.StatusServiceUnavailable, api.OneTrickError{
			Message: "Destiny 2 servers are currently unavailable",
			Status:  api.ErrDestinyServerDown,
		}
	default:
		return http.StatusInternalServerError, api.OneTrickError{
			Message: err.Error(),
			Status:  api.ErrInternalSystemDown,
		}
	}
}

func ToAPIStartFireteamSessionsError(err error) api.StartFireteamSessionsResponseObject {
	code, body := classifyFireteamSessionError(err)
	switch code {
	case http.StatusBadRequest:
		return api.StartFireteamSessions400JSONResponse(body)
	case http.StatusUnauthorized:
		return api.StartFireteamSessions401JSONResponse(body)
	case http.StatusForbidden:
		return api.StartFireteamSessions403JSONResponse(body)
	case http.StatusNotFound:
		return api.StartFireteamSessions404JSONResponse(body)
	case http.StatusServiceUnavailable:
		return api.StartFireteamSessions503JSONResponse(body)
	default:
		return api.StartFireteamSessions500JSONResponse(body)
	}
}

func ToAPIStartSessionError(err error) api.StartSessionResponseObject {
	code, body := classifyFireteamSessionError(err)
	switch code {
	case http.StatusBadRequest:
		return api.StartSession400JSONResponse{Message: body.Message}
	case http.StatusUnauthorized:
		return api.StartSession401JSONResponse(body)
	case http.StatusForbidden:
		return api.StartSession403JSONResponse(body)
	case http.StatusNotFound:
		return api.StartSession404JSONResponse(body)
	case http.StatusServiceUnavailable:
		return api.StartSession503JSONResponse(body)
	default:
		return api.StartSession500JSONResponse(body)
	}
}

func ToAPIStartUserSessionError(err error) api.StartUserSessionResponseObject {
	code, body := classifyFireteamSessionError(err)
	switch code {
	case http.StatusBadRequest:
		return api.StartUserSession400JSONResponse{Message: body.Message}
	case http.StatusUnauthorized:
		return api.StartUserSession401JSONResponse(body)
	case http.StatusForbidden:
		return api.StartUserSession403JSONResponse(body)
	case http.StatusNotFound:
		return api.StartUserSession404JSONResponse(body)
	case http.StatusServiceUnavailable:
		return api.StartUserSession503JSONResponse(body)
	default:
		return api.StartUserSession500JSONResponse(body)
	}
}
