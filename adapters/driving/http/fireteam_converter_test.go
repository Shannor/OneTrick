package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/fireteam"
	"testing"
	"time"
)

func TestToAPIFireteamMembers(t *testing.T) {
	startedAt := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)

	got := ToAPIFireteamMembers([]fireteam.Member{
		{
			ID:           "u-1",
			MembershipID: "4611686018467484863",
			DisplayName:  "GuardianOne",
			Characters: []fireteam.Character{
				{
					ID:                  "char-1",
					Class:               "Hunter",
					Race:                "Exo",
					CurrentTitle:        "Flawless",
					Light:               2010,
					EmblemURL:           "/emblem.png",
					EmblemBackgroundURL: "/bg.png",
					EmblemColor:         fireteam.Color{Red: 10, Green: 20, Blue: 30, Alpha: 255},
					Stats: map[string]fireteam.ClassStat{
						"mobility": {
							Name:            "Mobility",
							Description:     "Move speed",
							Icon:            "/icon.png",
							HasIcon:         true,
							StatCategory:    1,
							AggregationType: 2,
							Value:           100,
						},
					},
				},
			},
			ActiveSessions: []fireteam.ActiveSession{
				{
					SessionID:   "ses-1",
					UserID:      "u-1",
					CharacterID: "char-1",
					StartedAt:   startedAt,
					TrackComp:   true,
				},
			},
		},
		{
			ID:             "u-2",
			MembershipID:   "4611686018467484002",
			DisplayName:    "GuardianTwo",
			Characters:     nil,
			ActiveSessions: nil,
		},
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 members, got %d", len(got))
	}
	m0 := got[0]
	if m0.ID != "u-1" || len(m0.Characters) != 1 || m0.Characters[0].Id != "char-1" {
		t.Fatalf("unexpected first member: %+v", m0)
	}
	if m0.Characters[0].EmblemColor.Red != 10 || m0.Characters[0].Stats == nil || (*m0.Characters[0].Stats)["mobility"].Value != 100 {
		t.Fatalf("unexpected character conversion: %+v", m0.Characters[0])
	}
	if m0.ActiveSessions == nil || len(*m0.ActiveSessions) != 1 {
		t.Fatalf("expected 1 active session, got %+v", m0.ActiveSessions)
	}
	s0 := (*m0.ActiveSessions)[0]
	if s0.SessionID != "ses-1" || s0.CharacterID != "char-1" || !s0.StartedAt.Equal(startedAt) || s0.TrackComp == nil || !*s0.TrackComp {
		t.Fatalf("unexpected active session conversion: %+v", s0)
	}

	m1 := got[1]
	if m1.Characters == nil || len(m1.Characters) != 0 {
		t.Fatalf("expected non-nil empty characters slice, got %#v", m1.Characters)
	}
	if m1.ActiveSessions == nil || len(*m1.ActiveSessions) != 0 {
		t.Fatalf("expected non-nil empty activeSessions slice, got %#v", m1.ActiveSessions)
	}
}

func TestFireteamSessionConverters(t *testing.T) {
	startedAt := time.Date(2026, 5, 2, 18, 0, 0, 0, time.UTC)
	targets := ToStartSessionTargets([]api.FireteamSessionMemberTarget{
		{UserID: "u-1", CharacterID: "c-1"},
		{UserID: "u-2", CharacterID: "c-2"},
	})
	if len(targets) != 2 || targets[0].UserID != "u-1" || targets[1].CharacterID != "c-2" {
		t.Fatalf("unexpected converted targets: %+v", targets)
	}

	resp := ToAPIStartFireteamSessionsResponse([]fireteam.StartSessionResult{
		{
			UserID:      "u-1",
			CharacterID: "c-1",
			Outcome:     fireteam.StartSessionOutcomeStarted,
			Session: fireteam.Session{
				ID:          "ses-1",
				UserID:      "u-1",
				CharacterID: "c-1",
				Name:        "Flawless' Goes Brrr",
				Status:      fireteam.SessionStatusPending,
				StartedAt:   startedAt,
				StartedBy:   &fireteam.AuditField{ID: "u-1", Username: "Leader"},
				CompTracking: &fireteam.CompTracking{
					Enabled: true,
					Start: &fireteam.CompRankSnapshot{
						Points:       5800,
						DivisionName: "Platinum III",
					},
				},
			},
		},
		{
			UserID:      "u-2",
			CharacterID: "c-2",
			Outcome:     fireteam.StartSessionOutcomeSkippedActive,
			Session: fireteam.Session{
				ID:           "ses-2",
				UserID:       "u-2",
				CharacterID:  "c-2",
				Status:       fireteam.SessionStatusPending,
				StartedAt:    startedAt,
				AggregateIDs: []string{"agg-1"},
			},
		},
	})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	r0 := resp.Results[0]
	if r0.Outcome != api.FireteamSessionOutcomeStarted || r0.Session.AggregateIDs == nil || r0.Session.Status == nil || *r0.Session.Status != api.SessionPending {
		t.Fatalf("unexpected first result: %+v", r0)
	}
	if r0.Session.StartedBy == nil || r0.Session.StartedBy.Username != "Leader" {
		t.Fatalf("unexpected startedBy: %+v", r0.Session.StartedBy)
	}
	if r0.Session.CompTracking == nil || !r0.Session.CompTracking.Enabled || r0.Session.CompTracking.Start == nil || r0.Session.CompTracking.Start.Points != 5800 {
		t.Fatalf("unexpected compTracking: %+v", r0.Session.CompTracking)
	}
	if resp.Results[1].Outcome != api.FireteamSessionOutcomeSkippedActive || resp.Results[1].Session.ID != "ses-2" {
		t.Fatalf("unexpected second result: %+v", resp.Results[1])
	}
}

func TestFireteamSessionErrorConverters(t *testing.T) {
	if _, ok := ToAPIStartFireteamSessionsError(fireteam.ErrInvalidRequest).(api.StartFireteamSessions400JSONResponse); !ok {
		t.Fatalf("expected 400 for ErrInvalidRequest")
	}
	if _, ok := ToAPIStartFireteamSessionsError(fireteam.ErrUnauthorized).(api.StartFireteamSessions401JSONResponse); !ok {
		t.Fatalf("expected 401 for ErrUnauthorized")
	}
	if _, ok := ToAPIStartFireteamSessionsError(fireteam.ErrNotInFireteam).(api.StartFireteamSessions403JSONResponse); !ok {
		t.Fatalf("expected 403 for ErrNotInFireteam")
	}
	if _, ok := ToAPIStartFireteamSessionsError(fireteam.ErrUserNotFound).(api.StartFireteamSessions404JSONResponse); !ok {
		t.Fatalf("expected 404 for ErrUserNotFound")
	}
	if _, ok := ToAPIStartFireteamSessionsError(fireteam.ErrDestinyUnavailable).(api.StartFireteamSessions503JSONResponse); !ok {
		t.Fatalf("expected 503 for ErrDestinyUnavailable")
	}
	if _, ok := ToAPIStartSessionError(fireteam.ErrAlreadyActive).(api.StartSession400JSONResponse); !ok {
		t.Fatalf("expected 400 for StartSession ErrAlreadyActive")
	}
	if _, ok := ToAPIStartSessionError(fireteam.ErrNotInFireteam).(api.StartSession403JSONResponse); !ok {
		t.Fatalf("expected 403 for StartSession ErrNotInFireteam")
	}
	if _, ok := ToAPIStartUserSessionError(fireteam.ErrAlreadyActive).(api.StartUserSession400JSONResponse); !ok {
		t.Fatalf("expected 400 for StartUserSession ErrAlreadyActive")
	}
	if _, ok := ToAPIStartUserSessionError(fireteam.ErrDestinyUnavailable).(api.StartUserSession503JSONResponse); !ok {
		t.Fatalf("expected 503 for StartUserSession ErrDestinyUnavailable")
	}
}
