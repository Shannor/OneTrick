package session

import (
	"context"
	"oneTrick/api"
	"oneTrick/services/user"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

func statPair(val float64, display string) *api.StatsValuePair {
	p := &api.StatsValuePair{Value: new(val)}
	if display != "" {
		p.DisplayValue = new(display)
	}
	return p
}

func makeCompanionPerf(standing, team, fireteamID float64, fireteamDisplay string) api.InstancePerformance {
	ps := api.PlayerStats{
		Standing: statVal(standing),
		Kills:    statVal(10),
		Deaths:   statVal(5),
		Assists:  statVal(3),
	}
	if team >= 0 {
		ps.Team = statVal(team)
	}
	if fireteamID >= 0 {
		ps.FireTeamID = statPair(fireteamID, fireteamDisplay)
	}
	return api.InstancePerformance{
		PlayerStats: ps,
		Weapons: map[string]api.WeaponInstanceMetrics{
			"1001": weaponMetrics(1001, 5, "Rose", "/img/rose.png"),
		},
	}
}

func TestAreFireteamCompanions(t *testing.T) {
	tests := []struct {
		name string
		a    api.PlayerStats
		b    api.PlayerStats
		want bool
	}{
		{
			name: "matching non-zero fireteam and matching team",
			a:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(17), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(17), Standing: statVal(0)},
			want: true,
		},
		{
			name: "opposing team with same fireteam id is not a companion",
			a:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(17), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(18), Standing: statVal(1)},
			want: false,
		},
		{
			name: "different fireteam id on same team is random matchmade teammate",
			a:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(17), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99002, "99002"), Team: statVal(17), Standing: statVal(0)},
			want: false,
		},
		{
			name: "zero fireteam id is never a companion",
			a:    api.PlayerStats{FireTeamID: statPair(0, "0"), Team: statVal(17), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(0, "0"), Team: statVal(17), Standing: statVal(0)},
			want: false,
		},
		{
			name: "nil fireteam id is never a companion",
			a:    api.PlayerStats{Team: statVal(17), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(17), Standing: statVal(0)},
			want: false,
		},
		{
			name: "differing 64-bit fireteam display values do not falsely collide",
			a:    api.PlayerStats{FireTeamID: statPair(9007199254740992, "9007199254740992"), Team: statVal(17)},
			b:    api.PlayerStats{FireTeamID: statPair(9007199254740992, "9007199254740993"), Team: statVal(17)},
			want: false,
		},
		{
			name: "legacy pre-OT-6 aggregate without team falls back to matching standing",
			a:    api.PlayerStats{FireTeamID: statVal(777), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statVal(777), Standing: statVal(0)},
			want: true,
		},
		{
			name: "legacy pre-OT-6 aggregate without team rejects opposing standing",
			a:    api.PlayerStats{FireTeamID: statVal(777), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statVal(777), Standing: statVal(1)},
			want: false,
		},
		{
			name: "missing both team and standing rejects match",
			a:    api.PlayerStats{FireTeamID: statVal(777)},
			b:    api.PlayerStats{FireTeamID: statVal(777)},
			want: false,
		},
		{
			name: "FFA negative team sentinel (-1) is never a companion even with same fireteam id",
			a:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(-1), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(-1), Standing: statVal(0)},
			want: false,
		},
		{
			name: "single negative team sentinel (-1) does not fall back to standing",
			a:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Team: statVal(-1), Standing: statVal(0)},
			b:    api.PlayerStats{FireTeamID: statPair(99001, "99001"), Standing: statVal(0)},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := areFireteamCompanions(tc.a, tc.b); got != tc.want {
				t.Fatalf("areFireteamCompanions() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeSessionSummary_Companions(t *testing.T) {
	now := time.Date(2026, 4, 15, 21, 0, 0, 0, time.UTC)

	t.Run("aggregates friend swapping characters and excludes opponents randoms privates and detached links", func(t *testing.T) {
		aggSvc := newFakeSessionAggregateService()
		uSvc := &fakeSessionUserService{}

		// Match 1 (Win): A + B(Hunter, ses-B1) on same fireteam/team;
		// Opposing One Trick user D (team 18); Random teammate E (different fireteam);
		// Random teammate F (zero fireteam); Detached link G (nil sessionId).
		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-1",
				Mode:       new("Control"),
				IsPrivate:  new(false),
				Period:     now.Add(-30 * time.Minute),
			},
			SessionIds: []string{"ses-A", "ses-B1", "ses-D", "ses-E", "ses-F"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A", SessionID: new("ses-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"),
				},
				"char-B-hunter": {
					CharacterID: "char-B-hunter", SessionID: new("ses-B1"), UserID: new("user-B"),
					DisplayName: new("Bravo#2222"), CharacterClass: new("Hunter"), EmblemURL: new("/emblem/b-hunter.jpg"),
				},
				"char-D": {
					CharacterID: "char-D", SessionID: new("ses-D"), UserID: new("user-D"), DisplayName: new("Delta#4444"),
				},
				"char-E": {
					CharacterID: "char-E", SessionID: new("ses-E"), UserID: new("user-E"), DisplayName: new("Echo#5555"),
				},
				"char-F": {
					CharacterID: "char-F", SessionID: new("ses-F"), UserID: new("user-F"), DisplayName: new("Foxtrot#6666"),
				},
				"char-G": {
					CharacterID: "char-G", SessionID: nil, SnapshotID: new("snap-G"), UserID: new("user-G"), DisplayName: new("Ghost#7777"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":        makeCompanionPerf(0, 17, 5001, "5001"),
				"char-B-hunter": makeCompanionPerf(0, 17, 5001, "5001"),
				"char-D":        makeCompanionPerf(1, 18, 5001, "5001"),
				"char-E":        makeCompanionPerf(0, 17, 5002, "5002"),
				"char-F":        makeCompanionPerf(0, 17, 0, "0"),
				"char-G":        makeCompanionPerf(0, 17, 5001, "5001"),
			},
		})

		// Match 2 (Loss): A + B(Hunter, ses-B1) on same fireteam/team.
		aggSvc.seed(api.Aggregate{
			ID:         "agg-2",
			ActivityID: "act-2",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-2",
				Mode:       new("Control"),
				IsPrivate:  new(false),
				Period:     now.Add(-20 * time.Minute),
			},
			SessionIds: []string{"ses-A", "ses-B1"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A", SessionID: new("ses-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"),
				},
				"char-B-hunter": {
					CharacterID: "char-B-hunter", SessionID: new("ses-B1"), UserID: new("user-B"),
					DisplayName: new("Bravo#2222"), CharacterClass: new("Hunter"), EmblemURL: new("/emblem/b-hunter.jpg"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":        makeCompanionPerf(1, 17, 5001, "5001"),
				"char-B-hunter": makeCompanionPerf(1, 17, 5001, "5001"),
			},
		})

		// Match 3 (Win): B swapped to Warlock (ses-B2) under same user-B.
		aggSvc.seed(api.Aggregate{
			ID:         "agg-3",
			ActivityID: "act-3",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-3",
				Mode:       new("Control"),
				IsPrivate:  new(false),
				Period:     now.Add(-10 * time.Minute),
			},
			SessionIds: []string{"ses-A", "ses-B2"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A", SessionID: new("ses-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"),
				},
				"char-B-warlock": {
					CharacterID: "char-B-warlock", SessionID: new("ses-B2"), UserID: new("user-B"),
					DisplayName: new("Bravo#2222"), CharacterClass: new("Warlock"), EmblemURL: new("/emblem/b-warlock.jpg"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":         makeCompanionPerf(0, 17, 5001, "5001"),
				"char-B-warlock": makeCompanionPerf(0, 17, 5001, "5001"),
			},
		})

		// Match 4 (Private match): A + C share fireteam/team, but isPrivate == true -> MUST NOT record companion.
		aggSvc.seed(api.Aggregate{
			ID:         "agg-4",
			ActivityID: "act-4",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-4",
				Mode:       new("Clash"),
				IsPrivate:  new(true),
				Period:     now.Add(-5 * time.Minute),
			},
			SessionIds: []string{"ses-A", "ses-C"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A", SessionID: new("ses-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"),
				},
				"char-C": {
					CharacterID: "char-C", SessionID: new("ses-C"), UserID: new("user-C"),
					DisplayName: new("Charlie#3333"), CharacterClass: new("Titan"), EmblemURL: new("/emblem/c.jpg"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 6001, "6001"),
				"char-C": makeCompanionPerf(0, 17, 6001, "6001"),
			},
		})

		svc := &service{
			aggregateService: aggSvc,
			userService:      uSvc,
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		sesA := api.Session{
			ID:           "ses-A",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-1", "agg-2", "agg-3", "agg-4"},
		}
		summary, err := svc.ComputeSessionSummary(clog.NewContext(context.Background()), sesA)
		if err != nil {
			t.Fatalf("ComputeSessionSummary failed: %v", err)
		}
		if uSvc.findByCharCalls.Load() != 0 {
			t.Fatalf("expected 0 FindByCharacterIDs calls for stamped links, got %d", uSvc.findByCharCalls.Load())
		}
		if summary.TotalMatches == nil || *summary.TotalMatches != 4 {
			t.Fatalf("expected 4 total matches (including private match stats), got %v", summary.TotalMatches)
		}
		if summary.Companions == nil || len(*summary.Companions) != 1 {
			t.Fatalf("expected exactly 1 companion (user-B), got %+v", summary.Companions)
		}

		comp := (*summary.Companions)[0]
		if comp.UserID != "user-B" || comp.DisplayName != "Bravo#2222" {
			t.Fatalf("unexpected companion identity: %+v", comp)
		}
		if comp.SharedMatches != 3 || comp.Wins != 2 || comp.Losses != 1 {
			t.Fatalf("expected 3 shared matches (2W/1L), got %+v", comp)
		}
		if len(comp.Sessions) != 2 {
			t.Fatalf("expected 2 companion sessions (character swap), got %+v", comp.Sessions)
		}
		if comp.Sessions[0].SessionID != "ses-B1" || comp.Sessions[0].CharacterID != "char-B-hunter" ||
			comp.Sessions[0].SharedMatches != 2 || comp.Sessions[0].Class == nil || *comp.Sessions[0].Class != "Hunter" ||
			comp.Sessions[0].EmblemURL == nil || *comp.Sessions[0].EmblemURL != "/emblem/b-hunter.jpg" {
			t.Fatalf("unexpected first companion session: %+v", comp.Sessions[0])
		}
		if comp.Sessions[1].SessionID != "ses-B2" || comp.Sessions[1].CharacterID != "char-B-warlock" ||
			comp.Sessions[1].SharedMatches != 1 || comp.Sessions[1].Class == nil || *comp.Sessions[1].Class != "Warlock" ||
			comp.Sessions[1].EmblemURL == nil || *comp.Sessions[1].EmblemURL != "/emblem/b-warlock.jpg" {
			t.Fatalf("unexpected second companion session: %+v", comp.Sessions[1])
		}
	})

	t.Run("legacy unstamped links resolve via single batched FindByCharacterIDs lookup", func(t *testing.T) {
		aggSvc := newFakeSessionAggregateService()
		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"user-B": {
					ID:           "user-B",
					DisplayName:  "LegacyBravo#2222",
					CharacterIDs: []string{"char-B"},
					Characters: []api.Character{
						{Id: "char-B", Class: "Titan", EmblemURL: "/emblem/legacy-b.jpg"},
					},
				},
				"user-C": {
					ID:           "user-C",
					DisplayName:  "LegacyCharlie#3333",
					CharacterIDs: []string{"char-C"},
					Characters: []api.Character{
						{Id: "char-C", Class: "Warlock", EmblemURL: "/emblem/legacy-c.jpg"},
					},
				},
			},
		}

		// Legacy pre-OT-6 aggregate: no Team field (falls back to Standing) and no UserID on SnapshotLinks.
		aggSvc.seed(api.Aggregate{
			ID:         "agg-legacy",
			ActivityID: "act-legacy",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-legacy",
				Mode:       new("Trials of Osiris"),
				Period:     now.Add(-15 * time.Minute),
			},
			SessionIds: []string{"ses-A", "ses-B", "ses-C"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {CharacterID: "char-A", SessionID: new("ses-A")},
				"char-B": {CharacterID: "char-B", SessionID: new("ses-B")},
				"char-C": {CharacterID: "char-C", SessionID: new("ses-C")},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, -1, 8888, ""),
				"char-B": makeCompanionPerf(0, -1, 8888, ""),
				"char-C": makeCompanionPerf(0, -1, 8888, ""),
			},
		})

		svc := &service{
			aggregateService: aggSvc,
			userService:      uSvc,
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		sesA := api.Session{
			ID:           "ses-A",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-legacy"},
		}
		summary, err := svc.ComputeSessionSummary(clog.NewContext(context.Background()), sesA)
		if err != nil {
			t.Fatalf("ComputeSessionSummary failed: %v", err)
		}
		if uSvc.findByCharCalls.Load() != 1 {
			t.Fatalf("expected exactly 1 batched FindByCharacterIDs call for legacy links, got %d", uSvc.findByCharCalls.Load())
		}
		if summary.Companions == nil || len(*summary.Companions) != 2 {
			t.Fatalf("expected 2 resolved legacy companions, got %+v", summary.Companions)
		}
		first := (*summary.Companions)[0]
		second := (*summary.Companions)[1]
		if first.UserID != "user-B" || first.DisplayName != "LegacyBravo#2222" ||
			len(first.Sessions) != 1 || first.Sessions[0].Class == nil || *first.Sessions[0].Class != "Titan" {
			t.Fatalf("unexpected first legacy companion: %+v", first)
		}
		if second.UserID != "user-C" || second.DisplayName != "LegacyCharlie#3333" ||
			len(second.Sessions) != 1 || second.Sessions[0].Class == nil || *second.Sessions[0].Class != "Warlock" {
			t.Fatalf("unexpected second legacy companion: %+v", second)
		}
	})
}

func TestRunTick_TwoPhaseCompanionSync(t *testing.T) {
	now := time.Date(2026, 4, 15, 22, 0, 0, 0, time.UTC)

	newTwoStackFixture := func(order []string) (*service, *memorySessionStore, *fakeSessionUserService, *fakeSessionD2Service, *fakeSessionAggregateService) {
		store := newMemorySessionStore()
		store.candidateOrder = order
		store.docs["ses-A"] = &api.Session{
			ID:          "ses-A",
			UserID:      "user-A",
			CharacterID: "char-A",
			StartedAt:   now.Add(-30 * time.Minute),
			Status:      new(api.SessionPending),
		}
		store.docs["ses-B"] = &api.Session{
			ID:          "ses-B",
			UserID:      "user-B",
			CharacterID: "char-B",
			StartedAt:   now.Add(-30 * time.Minute),
			Status:      new(api.SessionPending),
		}

		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"user-A": {
					ID:                  "user-A",
					DisplayName:         "Alpha#1111",
					PrimaryMembershipID: "1001",
					Memberships:         []user.Membership{{ID: "1001", Type: 3}},
					CharacterIDs:        []string{"char-A"},
					Characters:          []api.Character{{Id: "char-A", Class: "Hunter", EmblemURL: "/emblem/a.jpg"}},
				},
				"user-B": {
					ID:                  "user-B",
					DisplayName:         "Bravo#2222",
					PrimaryMembershipID: "1002",
					Memberships:         []user.Membership{{ID: "1002", Type: 3}},
					CharacterIDs:        []string{"char-B"},
					Characters:          []api.Character{{Id: "char-B", Class: "Titan", EmblemURL: "/emblem/b.jpg"}},
				},
			},
		}

		sharedMatch := api.ActivityHistory{
			InstanceID: "act-shared-1",
			Activity:   "Control",
			Mode:       new("Control"),
			IsPrivate:  new(false),
			Period:     now.Add(-5 * time.Minute),
		}
		d2Svc := &fakeSessionD2Service{
			activitiesByChr: map[string][]api.ActivityHistory{
				"char-A": {sharedMatch},
				"char-B": {sharedMatch},
			},
			perfsByAct: map[string]map[string]api.InstancePerformance{
				"act-shared-1": {
					"char-A": makeCompanionPerf(0, 17, 4242, "4242"),
					"char-B": makeCompanionPerf(0, 17, 4242, "4242"),
				},
			},
		}

		aggSvc := newFakeSessionAggregateService()
		svc := &service{
			store:            store,
			aggregateService: aggSvc,
			userService:      uSvc,
			compRanks:        d2Svc,
			activityHistory:  d2Svc,
			performances:     d2Svc,
			snapshotService:  &fakeSessionSnapshotService{},
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}
		return svc, store, uSvc, d2Svc, aggSvc
	}

	assertSingleCompanion := func(t *testing.T, ses *api.Session, wantUserID, wantDisplay, wantSessionID, wantCharID, wantClass, wantEmblem string) {
		t.Helper()
		if ses.Summary == nil || ses.Summary.Companions == nil {
			t.Fatalf("session %s missing Summary.Companions: %+v", ses.ID, ses.Summary)
		}
		comps := *ses.Summary.Companions
		if len(comps) != 1 {
			t.Fatalf("session %s expected 1 companion, got %+v", ses.ID, comps)
		}
		c := comps[0]
		if c.UserID != wantUserID || c.DisplayName != wantDisplay || c.SharedMatches != 1 || c.Wins != 1 || c.Losses != 0 {
			t.Fatalf("session %s unexpected companion: %+v", ses.ID, c)
		}
		if len(c.Sessions) != 1 {
			t.Fatalf("session %s expected 1 companion session entry, got %+v", ses.ID, c.Sessions)
		}
		cs := c.Sessions[0]
		if cs.SessionID != wantSessionID || cs.CharacterID != wantCharID || cs.SharedMatches != 1 ||
			cs.Class == nil || *cs.Class != wantClass || cs.EmblemURL == nil || *cs.EmblemURL != wantEmblem {
			t.Fatalf("session %s unexpected companion session entry: %+v", ses.ID, cs)
		}
	}

	for _, tc := range []struct {
		name  string
		order []string
	}{
		{name: "order [A, B]", order: []string{"ses-A", "ses-B"}},
		{name: "order [B, A]", order: []string{"ses-B", "ses-A"}},
	} {
		t.Run("symmetric companions in single tick "+tc.name, func(t *testing.T) {
			svc, store, uSvc, _, _ := newTwoStackFixture(tc.order)

			if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
				t.Fatalf("RunTick failed: %v", err)
			}

			if uSvc.findByCharCalls.Load() != 0 {
				t.Fatalf("expected 0 FindByCharacterIDs calls when links are stamped, got %d", uSvc.findByCharCalls.Load())
			}

			assertSingleCompanion(t, store.docs["ses-A"], "user-B", "Bravo#2222", "ses-B", "char-B", "Titan", "/emblem/b.jpg")
			assertSingleCompanion(t, store.docs["ses-B"], "user-A", "Alpha#1111", "ses-A", "char-A", "Hunter", "/emblem/a.jpg")
		})
	}

	t.Run("staggered ticks recompute already-synced teammate session in phase 2", func(t *testing.T) {
		svc, store, _, d2Svc, _ := newTwoStackFixture([]string{"ses-A", "ses-B"})

		// Tick 1: Only Player A has the activity visible from Bungie yet.
		sharedMatch := d2Svc.activitiesByChr["char-A"][0]
		d2Svc.activitiesByChr["char-B"] = nil

		if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
			t.Fatalf("Tick 1 failed: %v", err)
		}
		if store.docs["ses-A"].Summary == nil || store.docs["ses-A"].Summary.Companions == nil || len(*store.docs["ses-A"].Summary.Companions) != 0 {
			t.Fatalf("expected 0 companions on ses-A after tick 1, got %+v", store.docs["ses-A"].Summary)
		}

		// Tick 2: Player A already saw act-shared-1 (skipped in Phase 1), Player B links it -> Phase 2 refreshes BOTH.
		d2Svc.activitiesByChr["char-B"] = []api.ActivityHistory{sharedMatch}
		if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
			t.Fatalf("Tick 2 failed: %v", err)
		}

		assertSingleCompanion(t, store.docs["ses-A"], "user-B", "Bravo#2222", "ses-B", "char-B", "Titan", "/emblem/b.jpg")
		assertSingleCompanion(t, store.docs["ses-B"], "user-A", "Alpha#1111", "ses-A", "char-A", "Hunter", "/emblem/a.jpg")
	})

	t.Run("non-candidate completed sibling session is fetched via GetDoc and updated in phase 2", func(t *testing.T) {
		svc, store, _, d2Svc, aggSvc := newTwoStackFixture([]string{"ses-B"})

		// Player A completed their session outside completedGracePeriod so it is NOT in GetTickCandidates.
		completedAt := now.Add(-completedGracePeriod - 30*time.Minute)
		store.docs["ses-A"].Status = new(api.SessionComplete)
		store.docs["ses-A"].CompletedAt = &completedAt
		store.docs["ses-A"].AggregateIDs = []string{"agg-existing"}
		store.candidateOrder = nil

		aggSvc.seed(api.Aggregate{
			ID:         "agg-existing",
			ActivityID: "act-shared-1",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-shared-1",
				Activity:   "Control",
				Mode:       new("Control"),
				IsPrivate:  new(false),
				Period:     now.Add(-5 * time.Minute),
			},
			CharacterIds: []string{"char-A"},
			SessionIds:   []string{"ses-A"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID:    "char-A",
					SessionID:      new("ses-A"),
					UserID:         new("user-A"),
					DisplayName:    new("Alpha#1111"),
					CharacterClass: new("Hunter"),
					EmblemURL:      new("/emblem/a.jpg"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 4242, "4242"),
			},
		})
		d2Svc.activitiesByChr["char-A"] = nil

		resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
		if err != nil {
			t.Fatalf("RunTick failed: %v", err)
		}
		if resp.TotalSessions != 1 {
			t.Fatalf("expected only 1 candidate (ses-B), got %d", resp.TotalSessions)
		}
		if store.getDocCalls.Load() == 0 {
			t.Fatalf("expected Phase 2 to fetch non-candidate sibling ses-A via GetDoc")
		}

		assertSingleCompanion(t, store.docs["ses-A"], "user-B", "Bravo#2222", "ses-B", "char-B", "Titan", "/emblem/b.jpg")
		assertSingleCompanion(t, store.docs["ses-B"], "user-A", "Alpha#1111", "ses-A", "char-A", "Hunter", "/emblem/a.jpg")
	})
}

func TestDeleteSession_RefreshesSiblingCompanions(t *testing.T) {
	now := time.Date(2026, 4, 15, 23, 0, 0, 0, time.UTC)

	t.Run("deleting player B session removes B from player A companions while keeping C intact", func(t *testing.T) {
		store := newMemorySessionStore()
		aggSvc := newFakeSessionAggregateService()

		// Match 1: 3-stack (A, B, C). Match 2: 2-stack (A, B).
		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-1",
				Mode:       new("Control"),
				Period:     now.Add(-20 * time.Minute),
			},
			CharacterIds: []string{"char-A", "char-B", "char-C"},
			SessionIds:   []string{"ses-A", "ses-B", "ses-C"},
			SnapshotIds:  []string{"snap-A", "snap-B", "snap-C"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {CharacterID: "char-A", SessionID: new("ses-A"), SnapshotID: new("snap-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"), CharacterClass: new("Hunter")},
				"char-B": {CharacterID: "char-B", SessionID: new("ses-B"), SnapshotID: new("snap-B"), UserID: new("user-B"), DisplayName: new("Bravo#2222"), CharacterClass: new("Titan")},
				"char-C": {CharacterID: "char-C", SessionID: new("ses-C"), SnapshotID: new("snap-C"), UserID: new("user-C"), DisplayName: new("Charlie#3333"), CharacterClass: new("Warlock")},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 7001, "7001"),
				"char-B": makeCompanionPerf(0, 17, 7001, "7001"),
				"char-C": makeCompanionPerf(0, 17, 7001, "7001"),
			},
		})
		aggSvc.seed(api.Aggregate{
			ID:         "agg-2",
			ActivityID: "act-2",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-2",
				Mode:       new("Control"),
				Period:     now.Add(-10 * time.Minute),
			},
			CharacterIds: []string{"char-A", "char-B"},
			SessionIds:   []string{"ses-A", "ses-B"},
			SnapshotIds:  []string{"snap-A", "snap-B"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {CharacterID: "char-A", SessionID: new("ses-A"), SnapshotID: new("snap-A"), UserID: new("user-A"), DisplayName: new("Alpha#1111"), CharacterClass: new("Hunter")},
				"char-B": {CharacterID: "char-B", SessionID: new("ses-B"), SnapshotID: new("snap-B"), UserID: new("user-B"), DisplayName: new("Bravo#2222"), CharacterClass: new("Titan")},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(1, 17, 7001, "7001"),
				"char-B": makeCompanionPerf(1, 17, 7001, "7001"),
			},
		})

		store.docs["ses-A"] = &api.Session{ID: "ses-A", UserID: "user-A", CharacterID: "char-A", AggregateIDs: []string{"agg-1", "agg-2"}, Status: new(api.SessionComplete)}
		store.docs["ses-B"] = &api.Session{ID: "ses-B", UserID: "user-B", CharacterID: "char-B", AggregateIDs: []string{"agg-1", "agg-2"}, Status: new(api.SessionComplete)}
		store.docs["ses-C"] = &api.Session{ID: "ses-C", UserID: "user-C", CharacterID: "char-C", AggregateIDs: []string{"agg-1"}, Status: new(api.SessionComplete)}

		svc := &service{
			store:            store,
			aggregateService: aggSvc,
			userService:      &fakeSessionUserService{},
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		// Compute initial summaries so ses-A starts with both B and C as companions.
		initSummaryA, err := svc.ComputeSessionSummary(clog.NewContext(context.Background()), *store.docs["ses-A"])
		if err != nil {
			t.Fatalf("initial ComputeSessionSummary failed: %v", err)
		}
		if initSummaryA.Companions == nil || len(*initSummaryA.Companions) != 2 {
			t.Fatalf("expected 2 initial companions on ses-A, got %+v", initSummaryA.Companions)
		}
		store.docs["ses-A"].Summary = initSummaryA

		if err := svc.Delete(clog.NewContext(context.Background()), "ses-B", "user-B"); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		if _, exists := store.docs["ses-B"]; exists {
			t.Fatalf("expected ses-B to be deleted")
		}

		// Player A should now only have Player C as a companion (Player B removed even though snap-B still exists).
		sumA := store.docs["ses-A"].Summary
		if sumA == nil || sumA.Companions == nil || len(*sumA.Companions) != 1 {
			t.Fatalf("expected 1 companion remaining on ses-A after deleting ses-B, got %+v", sumA)
		}
		if (*sumA.Companions)[0].UserID != "user-C" || (*sumA.Companions)[0].SharedMatches != 1 {
			t.Fatalf("expected only user-C remaining on ses-A, got %+v", *sumA.Companions)
		}

		// Player C should now only have Player A as a companion.
		sumC := store.docs["ses-C"].Summary
		if sumC == nil || sumC.Companions == nil || len(*sumC.Companions) != 1 || (*sumC.Companions)[0].UserID != "user-A" {
			t.Fatalf("expected only user-A remaining on ses-C, got %+v", sumC)
		}
	})

	t.Run("missing sibling session document is ignored cleanly during delete cleanup", func(t *testing.T) {
		store := newMemorySessionStore()
		aggSvc := newFakeSessionAggregateService()

		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			SessionIds: []string{"ses-B", "ses-ghost"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-B":     {CharacterID: "char-B", SessionID: new("ses-B"), UserID: new("user-B")},
				"char-ghost": {CharacterID: "char-ghost", SessionID: new("ses-ghost"), UserID: new("user-ghost")},
			},
		})
		store.docs["ses-B"] = &api.Session{ID: "ses-B", UserID: "user-B", CharacterID: "char-B", AggregateIDs: []string{"agg-1"}}

		svc := &service{
			store:            store,
			aggregateService: aggSvc,
			userService:      &fakeSessionUserService{},
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		if err := svc.Delete(clog.NewContext(context.Background()), "ses-B", "user-B"); err != nil {
			t.Fatalf("Delete should ignore ErrNotFound sibling session, got %v", err)
		}
		if _, exists := store.docs["ses-B"]; exists {
			t.Fatalf("expected ses-B to be deleted")
		}
	})
}
