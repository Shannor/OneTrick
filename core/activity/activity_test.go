package activity

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type fakeCarnageGateway struct {
	report CarnageReport
	err    error
	calls  int
}

func (f *fakeCarnageGateway) GetPostGameCarnageReport(_ context.Context, _ string) (CarnageReport, error) {
	f.calls++
	if f.err != nil {
		return CarnageReport{}, f.err
	}
	return f.report, nil
}

type fakeManifestLookup struct {
	items        map[int64]ItemManifest
	itemsErr     error
	itemsCalls   int
	requestedIDs []int64

	activities      map[int64]ActivityManifest
	activitiesErr   error
	activitiesCalls int

	mode      ActivityModeManifest
	modeFound bool
	modeErr   error
	modeCalls int
}

func (f *fakeManifestLookup) GetItemsByIDs(_ context.Context, ids []int64) (map[int64]ItemManifest, error) {
	f.itemsCalls++
	f.requestedIDs = append(f.requestedIDs, ids...)
	if f.itemsErr != nil {
		return nil, f.itemsErr
	}
	return f.items, nil
}

func (f *fakeManifestLookup) GetActivitiesByIDs(_ context.Context, _ []int64) (map[int64]ActivityManifest, error) {
	f.activitiesCalls++
	if f.activitiesErr != nil {
		return nil, f.activitiesErr
	}
	return f.activities, nil
}

func (f *fakeManifestLookup) GetActivityMode(_ context.Context, _ int64, _ int) (ActivityModeManifest, bool, error) {
	f.modeCalls++
	if f.modeErr != nil {
		return ActivityModeManifest{}, false, f.modeErr
	}
	return f.mode, f.modeFound, nil
}

type fakeAggregateReader struct {
	agg   Aggregate
	found bool
	err   error
	calls int
}

func (f *fakeAggregateReader) GetByActivityID(_ context.Context, _ string) (Aggregate, bool, error) {
	f.calls++
	if f.err != nil {
		return Aggregate{}, false, f.err
	}
	return f.agg, f.found, nil
}

type fakeSnapshotReader struct {
	snaps      []CharacterSnapshot
	err        error
	calls      int
	capturedID []string
}

func (f *fakeSnapshotReader) GetByIDs(_ context.Context, snapshotIDs []string) ([]CharacterSnapshot, error) {
	f.calls++
	f.capturedID = append(f.capturedID, snapshotIDs...)
	if f.err != nil {
		return nil, f.err
	}
	return f.snaps, nil
}

type fakeUserLookup struct {
	partyUsers        []User
	partyErr          error
	partyCalls        int
	capturedMemberIDs []string

	byIDUsers       []User
	byIDErr         error
	byIDCalls       int
	capturedUserIDs []string
}

func (f *fakeUserLookup) FindByPartyMembershipIDs(_ context.Context, membershipIDs []string) ([]User, error) {
	f.partyCalls++
	f.capturedMemberIDs = append(f.capturedMemberIDs, membershipIDs...)
	if f.partyErr != nil {
		return nil, f.partyErr
	}
	return f.partyUsers, nil
}

func (f *fakeUserLookup) GetByIDs(_ context.Context, userIDs []string) ([]User, error) {
	f.byIDCalls++
	f.capturedUserIDs = append(f.capturedUserIDs, userIDs...)
	if f.byIDErr != nil {
		return nil, f.byIDErr
	}
	return f.byIDUsers, nil
}

func stat(v float64, display string) StatValuePair {
	return StatValuePair{Value: v, DisplayValue: display, Present: true}
}

func TestGetActivity_TeamMatchFullRosterAndTrackingMetadata(t *testing.T) {
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)

	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Period: now,
			ActivityDetails: CarnageActivityDetails{
				ReferenceID:          100,
				DirectorActivityHash: 200,
				InstanceID:           "999001",
				Mode:                 10,
				ModeName:             "Control",
			},
			Teams: []Team{
				{ID: "16", Standing: "Victory", Score: "150", TeamName: "Alpha"},
				{ID: "17", Standing: "Defeat", Score: "120", TeamName: "Bravo"},
			},
			Entries: []CarnageEntry{
				// Alpha lower score (should sort 2nd on Alpha) — Session-only noMatch player
				{
					CharacterID:     "char-nomatch",
					MembershipID:    "mem-nomatch",
					MembershipType:  3,
					DisplayName:     "SessionOnly",
					DisplayNameCode: "0042",
					CharacterClass:  "Titan",
					LightLevel:      2010,
					EmblemHash:      9002,
					Standing:        0,
					HasStanding:     true,
					Score:           stat(18, "18"),
					Completed:       true,
					PlayerStats: PlayerStats{
						Kills:      stat(14, "14"),
						Assists:    stat(6, "6"),
						Deaths:     stat(10, "10"),
						KDA:        stat(1.7, "1.70"),
						Team:       stat(16, "16"),
						FireteamID: stat(5550001, "5550001"),
					},
					Weapons: []CarnageWeapon{
						{ReferenceID: 2001, Stats: map[string]UniqueStatValue{"uniqueWeaponKills": {Basic: stat(9, "9")}}},
					},
				},
				// Alpha higher score (should sort 1st on Alpha) — Matched Snapshot player
				{
					CharacterID:     "char-snap",
					MembershipID:    "mem-snap",
					MembershipType:  3,
					DisplayName:     "Weegee",
					DisplayNameCode: "1234",
					CharacterClass:  "Hunter",
					LightLevel:      2020,
					EmblemHash:      9001,
					Standing:        0,
					HasStanding:     true,
					Score:           stat(28, "28"),
					Completed:       true,
					PlayerStats: PlayerStats{
						Kills:      stat(24, "24"),
						Assists:    stat(8, "8"),
						Deaths:     stat(6, "6"),
						KDA:        stat(4.67, "4.67"),
						Team:       stat(16, "16"),
						FireteamID: stat(5550001, "5550001"),
					},
					Weapons: []CarnageWeapon{
						{ReferenceID: 1001, Stats: map[string]UniqueStatValue{"uniqueWeaponKills": {Basic: stat(15, "15")}}},
						{ReferenceID: 2001, Stats: map[string]UniqueStatValue{"uniqueWeaponKills": {Basic: stat(7, "7")}}},
					},
				},
				// Alpha 3rd — Registered 1 Trick user without active session
				{
					CharacterID:     "char-reg",
					MembershipID:    "mem-reg",
					MembershipType:  2,
					DisplayName:     "RegisteredNoSession",
					DisplayNameCode: "7777",
					CharacterClass:  "Warlock",
					LightLevel:      2005,
					EmblemHash:      9003,
					Standing:        0,
					HasStanding:     true,
					Score:           stat(12, "12"),
					Completed:       true,
					PlayerStats: PlayerStats{
						Kills:      stat(10, "10"),
						Deaths:     stat(9, "9"),
						KDA:        stat(1.2, "1.20"),
						Team:       stat(16, "16"),
						FireteamID: stat(0, "0"),
					},
					Weapons: []CarnageWeapon{
						{ReferenceID: 3001, Stats: map[string]UniqueStatValue{"uniqueWeaponKills": {Basic: stat(4, "4")}}},
					},
				},
				// Bravo 1st — Untracked opponent, tie-break by kills/KDA
				{
					CharacterID:     "char-opp-1",
					MembershipID:    "mem-opp-1",
					MembershipType:  1,
					DisplayName:     "RivalOne",
					DisplayNameCode: "4321",
					CharacterClass:  "Hunter",
					LightLevel:      2015,
					EmblemHash:      9001, // duplicate emblem hash should be deduplicated
					Standing:        1,
					HasStanding:     true,
					Score:           stat(20, "20"),
					Completed:       true,
					PlayerStats: PlayerStats{
						Kills:      stat(19, "19"),
						Deaths:     stat(11, "11"),
						KDA:        stat(2.0, "2.00"),
						Team:       stat(17, "17"),
						FireteamID: stat(8880002, "8880002"),
					},
					Weapons: []CarnageWeapon{
						{ReferenceID: 1001, Stats: map[string]UniqueStatValue{"uniqueWeaponKills": {Basic: stat(12, "12")}}},
					},
				},
				// Bravo 2nd — Same score as RivalOne, lower kills
				{
					CharacterID:    "char-opp-2",
					MembershipID:   "mem-opp-2",
					MembershipType: 3,
					DisplayName:    "RivalTwo",
					CharacterClass: "Titan",
					LightLevel:     1990,
					EmblemHash:     9004,
					Standing:       1,
					HasStanding:    true,
					Score:          stat(20, "20"),
					Completed:      true,
					PlayerStats: PlayerStats{
						Kills:  stat(15, "15"),
						Deaths: stat(12, "12"),
						KDA:    stat(1.5, "1.50"),
						Team:   stat(17, "17"),
					},
				},
				// Bravo 3rd — DNF opponent (completed=false)
				{
					CharacterID:     "char-opp-dnf",
					MembershipID:    "mem-opp-dnf",
					MembershipType:  3,
					DisplayName:     "RageQuitter",
					DisplayNameCode: "9999",
					CharacterClass:  "Warlock",
					LightLevel:      1980,
					Standing:        1,
					HasStanding:     true,
					Score:           stat(2, "2"),
					Completed:       false,
					PlayerStats: PlayerStats{
						Kills:  stat(1, "1"),
						Deaths: stat(8, "8"),
						KDA:    stat(0.12, "0.12"),
						Team:   stat(17, "17"),
					},
				},
			},
			RawEntries: []map[string]any{{"CharacterId": "char-snap"}},
		},
	}

	manifest := &fakeManifestLookup{
		items: map[int64]ItemManifest{
			1001: {
				Hash:                       1001,
				Name:                       "Rose",
				IconURL:                    "https://www.bungie.net/img/rose.png",
				HasIcon:                    true,
				ItemTypeDisplayName:        "Hand Cannon",
				ItemTypeAndTierDisplayName: "Legendary Hand Cannon",
				BucketHash:                 1498876634,
			},
			2001: {
				Hash:                       2001,
				Name:                       "Matador 64",
				IconURL:                    "https://www.bungie.net/img/matador.png",
				HasIcon:                    true,
				ItemTypeDisplayName:        "Shotgun",
				ItemTypeAndTierDisplayName: "Legendary Shotgun",
				BucketHash:                 2465295065,
			},
			3001: {
				Hash:                       3001,
				Name:                       "Cataphract GL3",
				IconURL:                    "https://www.bungie.net/img/cataphract.png",
				HasIcon:                    true,
				ItemTypeDisplayName:        "Grenade Launcher",
				ItemTypeAndTierDisplayName: "Legendary Grenade Launcher",
				BucketHash:                 953998645,
			},
			9001: {Hash: 9001, IconURL: "https://www.bungie.net/img/emblem1.png", HasIcon: true},
			9002: {Hash: 9002, IconURL: "https://www.bungie.net/img/emblem2.png", HasIcon: true},
			9003: {Hash: 9003, IconURL: "https://www.bungie.net/img/emblem3.png", HasIcon: true},
			9004: {Hash: 9004, IconURL: "https://www.bungie.net/img/emblem4.png", HasIcon: true},
		},
	}

	enrichedRoseProps := &ItemProperties{
		BaseInfo: BaseItemInfo{
			Name:                "Rose",
			ItemHash:            1001,
			InstanceID:          "inst-rose-1",
			BucketHash:          1498876634,
			ItemTypeDisplayName: "Hand Cannon",
		},
		Perks: []Perk{{Name: "Slideshot", Hash: 55}},
	}

	aggReader := &fakeAggregateReader{
		found: true,
		agg: Aggregate{
			ID:         "agg-1",
			ActivityID: "999001",
			ActivityDetails: ActivityInfo{
				InstanceID:        "999001",
				Location:          "Javelin-4",
				Activity:          "Control",
				EstimatedGameMode: "quickplay",
				Period:            now,
			},
			SnapshotLinks: map[string]SnapshotLink{
				"char-snap": {
					CharacterID:     "char-snap",
					SnapshotID:      "snap-1",
					SessionID:       "ses-1",
					ConfidenceLevel: ConfidenceHigh,
					UserID:          "user-snap",
				},
				"char-nomatch": {
					CharacterID:     "char-nomatch",
					SnapshotID:      "",
					SessionID:       "ses-2",
					ConfidenceLevel: ConfidenceNoMatch,
					UserID:          "user-nomatch",
				},
			},
			Performance: map[string]Performance{
				"char-snap": {
					Weapons: map[string]WeaponMetrics{
						"1001": {
							ReferenceID: 1001,
							Properties:  enrichedRoseProps,
						},
					},
				},
			},
		},
	}

	snapReader := &fakeSnapshotReader{
		snaps: []CharacterSnapshot{
			{
				ID:          "snap-1",
				UserID:      "user-snap",
				CharacterID: "char-snap",
				Name:        "Trials Rose Loadout",
			},
		},
	}

	userLookup := &fakeUserLookup{
		partyUsers: []User{
			{ID: "user-snap", PrimaryMembershipID: "mem-snap", DisplayName: "Weegee"},
			{ID: "user-nomatch", PrimaryMembershipID: "mem-nomatch", DisplayName: "SessionOnly"},
			{ID: "user-reg", PrimaryMembershipID: "mem-reg", DisplayName: "RegisteredNoSession"},
		},
	}

	svc := NewService(carnage, manifest, aggReader, snapReader, userLookup)

	ctx := clog.NewContext(context.Background())
	report, err := svc.GetActivity(ctx, "999001")
	if err != nil {
		t.Fatalf("GetActivity returned error: %v", err)
	}

	// Single batched Manifest call for both weapons and emblems.
	if manifest.itemsCalls != 1 {
		t.Fatalf("expected 1 batched GetItemsByIDs call, got %d", manifest.itemsCalls)
	}
	wantHashes := []int64{9002, 2001, 9001, 1001, 9003, 3001, 9004}
	if !slices.Equal(manifest.requestedIDs, wantHashes) {
		t.Fatalf("unexpected manifest hashes: got %v, want %v", manifest.requestedIDs, wantHashes)
	}

	// Zero N+1 user reads: 1 batched FindByPartyMembershipIDs call, 0 fallback GetByIDs calls.
	if userLookup.partyCalls != 1 {
		t.Fatalf("expected 1 batched FindByPartyMembershipIDs call, got %d", userLookup.partyCalls)
	}
	if userLookup.byIDCalls != 0 {
		t.Fatalf("expected 0 fallback GetByIDs calls when all users matched by membership, got %d", userLookup.byIDCalls)
	}

	// Full 6-player roster returned in deterministic team + performance order.
	if len(report.Participants) != 6 {
		t.Fatalf("expected 6 participants, got %d", len(report.Participants))
	}
	gotOrder := make([]string, 0, len(report.Participants))
	for _, p := range report.Participants {
		gotOrder = append(gotOrder, p.CharacterID)
	}
	wantOrder := []string{"char-snap", "char-nomatch", "char-reg", "char-opp-1", "char-opp-2", "char-opp-dnf"}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("unexpected participant sort order: got %v, want %v", gotOrder, wantOrder)
	}

	// Verify matched snapshot participant (1st)
	snapP := report.Participants[0]
	if snapP.UserID != "user-snap" || snapP.SnapshotID != "snap-1" || snapP.SessionID != "ses-1" || snapP.ConfidenceLevel != ConfidenceHigh {
		t.Fatalf("unexpected snapshot participant linkage: %+v", snapP)
	}
	if snapP.EmblemIconURL != "https://www.bungie.net/img/emblem1.png" {
		t.Fatalf("expected hydrated emblem URL, got %q", snapP.EmblemIconURL)
	}
	if snapP.FireteamID != "5550001" {
		t.Fatalf("expected fireteamId 5550001, got %q", snapP.FireteamID)
	}
	rose := snapP.Performance.Weapons["1001"]
	if rose.BucketHash != 1498876634 || rose.ItemTypeDisplayName != "Hand Cannon" || rose.Display.Name != "Rose" {
		t.Fatalf("expected Manifest-hydrated Rose weapon metadata, got %+v", rose)
	}
	if rose.Properties == nil || rose.Properties.BaseInfo.InstanceID != "inst-rose-1" {
		t.Fatalf("expected snapshot-enriched weapon properties preserved on Rose, got %+v", rose.Properties)
	}
	matador := snapP.Performance.Weapons["2001"]
	if matador.BucketHash != 2465295065 || matador.ItemTypeDisplayName != "Shotgun" || matador.Properties != nil {
		t.Fatalf("expected Manifest-hydrated non-enriched Matador metadata, got %+v", matador)
	}

	// Verify session-only noMatch participant (2nd)
	noMatchP := report.Participants[1]
	if noMatchP.UserID != "user-nomatch" || noMatchP.SessionID != "ses-2" || noMatchP.ConfidenceLevel != ConfidenceNoMatch || noMatchP.SnapshotID != "" {
		t.Fatalf("unexpected noMatch participant linkage: %+v", noMatchP)
	}
	if _, ok := report.Users["char-nomatch"]; !ok {
		t.Fatal("expected users[char-nomatch] populated for session-only noMatch player")
	}
	if _, ok := report.Snapshots["char-nomatch"]; ok {
		t.Fatal("did not expect snapshots[char-nomatch] for noMatch player")
	}

	// Verify registered user without active session (3rd)
	regP := report.Participants[2]
	if regP.UserID != "user-reg" || regP.SessionID != "" || regP.SnapshotID != "" || regP.ConfidenceLevel != "" {
		t.Fatalf("unexpected registered-without-session participant linkage: %+v", regP)
	}
	if regP.FireteamID != "" {
		t.Fatalf("expected zero fireteamId to be omitted, got %q", regP.FireteamID)
	}
	if _, ok := report.Users["char-reg"]; !ok {
		t.Fatal("expected users[char-reg] populated for registered 1 Trick user without active session")
	}

	// Verify DNF opponent (6th)
	dnfP := report.Participants[5]
	if dnfP.Completed {
		t.Fatal("expected DNF opponent to have Completed=false")
	}
	if dnfP.UserID != "" || dnfP.SessionID != "" || dnfP.SnapshotID != "" {
		t.Fatalf("expected untracked opponent to have no 1 Trick linkage, got %+v", dnfP)
	}

	group := findClogGroupFields(clog.BagFromContext(ctx).Attrs(), "activityService", "getActivity")
	if group == nil {
		t.Fatal("expected activityService.getActivity clog group")
	}
	if got := group["participantCount"]; got != int64(6) {
		t.Fatalf("expected participantCount=6, got %v (%T)", got, got)
	}
	if got := group["trackedSnapshotCount"]; got != int64(1) {
		t.Fatalf("expected trackedSnapshotCount=1, got %v (%T)", got, got)
	}
	if got := group["trackedSessionCount"]; got != int64(2) {
		t.Fatalf("expected trackedSessionCount=2, got %v (%T)", got, got)
	}
	if got := group["registeredUserCount"]; got != int64(3) {
		t.Fatalf("expected registeredUserCount=3, got %v (%T)", got, got)
	}
}

func findClogGroupFields(attrs []slog.Attr, path ...string) map[string]any {
	current := attrs
	for _, key := range path {
		var next []slog.Attr
		found := false
		for _, attr := range current {
			if attr.Key == key && attr.Value.Kind() == slog.KindGroup {
				next = attr.Value.Group()
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		current = next
	}
	fields := make(map[string]any, len(current))
	for _, attr := range current {
		fields[attr.Key] = attr.Value.Any()
	}
	return fields
}

func TestGetActivity_FFARumbleSortingAndTeamOmission(t *testing.T) {
	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Teams: []Team{},
			Entries: []CarnageEntry{
				{
					CharacterID: "char-3rd",
					Standing:    2,
					HasStanding: true,
					Score:       stat(14, "14"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(14, "14"),
						Team:  stat(-1, "-1"),
					},
				},
				{
					CharacterID: "char-1st",
					Standing:    0,
					HasStanding: true,
					Score:       stat(20, "20"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(20, "20"),
						Team:  stat(-1, "-1"),
					},
				},
				{
					CharacterID: "char-2nd",
					Standing:    1,
					HasStanding: true,
					Score:       stat(18, "18"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(18, "18"),
						Team:  stat(-1, "-1"),
					},
				},
			},
		},
	}

	svc := NewService(carnage, &fakeManifestLookup{}, &fakeAggregateReader{found: false}, &fakeSnapshotReader{}, &fakeUserLookup{})
	report, err := svc.GetActivity(context.Background(), "123456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Participants) != 3 {
		t.Fatalf("expected 3 FFA participants, got %d", len(report.Participants))
	}
	gotOrder := []string{report.Participants[0].CharacterID, report.Participants[1].CharacterID, report.Participants[2].CharacterID}
	wantOrder := []string{"char-1st", "char-2nd", "char-3rd"}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("unexpected FFA sort order: got %v, want %v", gotOrder, wantOrder)
	}
	for _, p := range report.Participants {
		if p.TeamID != "" {
			t.Fatalf("expected empty TeamID for FFA participant %s, got %q", p.CharacterID, p.TeamID)
		}
	}
}

func TestGetActivity_MissingAggregateFallbackToPGCRAndManifest(t *testing.T) {
	period := time.Date(2026, 11, 20, 12, 30, 0, 0, time.UTC)
	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Period: period,
			ActivityDetails: CarnageActivityDetails{
				ReferenceID:          5001,
				DirectorActivityHash: 6001,
				InstanceID:           "777888",
				Mode:                 92,
				ModeName:             "Iron Banner",
				IsPrivate:            false,
				HasIsPrivate:         true,
			},
			Teams: []Team{
				{ID: "16", Standing: "Victory", Score: "100", TeamName: "Alpha"},
			},
			Entries: []CarnageEntry{
				{
					CharacterID:  "char-1",
					MembershipID: "mem-1",
					DisplayName:  "SoloPlayer",
					EmblemHash:   8001,
					Completed:    true,
					PlayerStats:  PlayerStats{Team: stat(16, "16")},
				},
			},
		},
	}

	manifest := &fakeManifestLookup{
		activities: map[int64]ActivityManifest{
			5001: {
				Hash:         5001,
				Name:         "Burnout",
				Description:  "Fields of Glass",
				PgcrImageURL: "https://www.bungie.net/img/burnout.jpg",
			},
			6001: {
				Hash:                   6001,
				Name:                   "Iron Banner: Tribute",
				DirectActivityModeHash: 7001,
			},
		},
		mode: ActivityModeManifest{
			Hash:     7001,
			ModeType: 92,
			IconURL:  "https://www.bungie.net/img/ib-icon.png",
		},
		modeFound: true,
		items: map[int64]ItemManifest{
			8001: {Hash: 8001, IconURL: "https://www.bungie.net/img/emblem8001.png", HasIcon: true},
		},
	}

	userLookup := &fakeUserLookup{
		partyUsers: []User{
			{ID: "user-1", PrimaryMembershipID: "mem-1", DisplayName: "SoloPlayer"},
		},
	}

	svc := NewService(carnage, manifest, &fakeAggregateReader{found: false}, &fakeSnapshotReader{}, userLookup)
	report, err := svc.GetActivity(context.Background(), "777888")
	if err != nil {
		t.Fatalf("expected 200 fallback when aggregate missing, got error: %v", err)
	}
	if report.Aggregate != nil {
		t.Fatalf("expected nil Aggregate when not found, got %+v", report.Aggregate)
	}
	if report.Snapshots == nil || len(report.Snapshots) != 0 {
		t.Fatalf("expected empty non-nil Snapshots map, got %+v", report.Snapshots)
	}
	if report.Activity.Location != "Burnout" || report.Activity.Activity != "Iron Banner: Tribute" {
		t.Fatalf("unexpected fallback Activity metadata: %+v", report.Activity)
	}
	if report.Activity.Mode != "Iron Banner Tribute" || report.Activity.EstimatedGameMode != "ironBanner" {
		t.Fatalf("expected Iron Banner Tribute mode and ironBanner estimated mode, got mode=%q est=%q", report.Activity.Mode, report.Activity.EstimatedGameMode)
	}
	if report.Activity.ImageURL != "https://www.bungie.net/img/burnout.jpg" || report.Activity.ActivityIcon != "https://www.bungie.net/img/ib-icon.png" {
		t.Fatalf("unexpected fallback activity URLs: %+v", report.Activity)
	}
	if !report.Activity.Period.Equal(period) {
		t.Fatalf("expected Period=%v, got %v", period, report.Activity.Period)
	}
	if len(report.Participants) != 1 || report.Participants[0].UserID != "user-1" {
		t.Fatalf("expected registered user still resolved without aggregate, got %+v", report.Participants)
	}
}

func TestGetActivity_BatchedFallbackUserLookupByIDForSessionAndSnapshot(t *testing.T) {
	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Entries: []CarnageEntry{
				{
					CharacterID:  "char-notfound",
					MembershipID: "mem-unmatched",
					DisplayName:  "AltMembership",
					Completed:    true,
				},
			},
		},
	}

	aggReader := &fakeAggregateReader{
		found: true,
		agg: Aggregate{
			ID:         "agg-2",
			ActivityID: "555",
			SnapshotLinks: map[string]SnapshotLink{
				"char-notfound": {
					CharacterID:     "char-notfound",
					SessionID:       "ses-nf",
					ConfidenceLevel: ConfidenceNotFound,
					UserID:          "user-nf",
					EmblemURL:       "https://www.bungie.net/img/fallback-emblem.png",
					CharacterClass:  "Hunter",
				},
			},
		},
	}

	userLookup := &fakeUserLookup{
		partyUsers: []User{},
		byIDUsers: []User{
			{ID: "user-nf", DisplayName: "AltMembership", PrimaryMembershipID: "mem-other"},
		},
	}

	svc := NewService(carnage, &fakeManifestLookup{}, aggReader, &fakeSnapshotReader{}, userLookup)
	report, err := svc.GetActivity(context.Background(), "555")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userLookup.byIDCalls != 1 || !slices.Equal(userLookup.capturedUserIDs, []string{"user-nf"}) {
		t.Fatalf("expected 1 batched GetByIDs call for [user-nf], got calls=%d ids=%v", userLookup.byIDCalls, userLookup.capturedUserIDs)
	}
	p := report.Participants[0]
	if p.UserID != "user-nf" || p.SessionID != "ses-nf" || p.ConfidenceLevel != ConfidenceNotFound {
		t.Fatalf("unexpected participant tracking fields: %+v", p)
	}
	if p.EmblemIconURL != "https://www.bungie.net/img/fallback-emblem.png" || p.CharacterClass != "Hunter" {
		t.Fatalf("expected SnapshotLink fallback emblem/class, got emblem=%q class=%q", p.EmblemIconURL, p.CharacterClass)
	}
	if _, ok := report.Users["char-notfound"]; !ok {
		t.Fatal("expected users[char-notfound] populated via batched GetByIDs")
	}
}

func TestGetActivity_ValidationAndErrors(t *testing.T) {
	t.Run("empty activityId", func(t *testing.T) {
		svc := NewService(&fakeCarnageGateway{}, nil, nil, nil, nil)
		_, err := svc.GetActivity(context.Background(), "   ")
		if !errors.Is(err, ErrInvalidActivityID) {
			t.Fatalf("expected ErrInvalidActivityID, got %v", err)
		}
	})

	t.Run("non-numeric activityId", func(t *testing.T) {
		svc := NewService(&fakeCarnageGateway{}, nil, nil, nil, nil)
		_, err := svc.GetActivity(context.Background(), "abc-not-an-int")
		if !errors.Is(err, ErrInvalidActivityID) {
			t.Fatalf("expected ErrInvalidActivityID, got %v", err)
		}
	})

	t.Run("activity not found propagated", func(t *testing.T) {
		svc := NewService(&fakeCarnageGateway{err: ErrActivityNotFound}, nil, nil, nil, nil)
		_, err := svc.GetActivity(context.Background(), "12345")
		if !errors.Is(err, ErrActivityNotFound) {
			t.Fatalf("expected ErrActivityNotFound, got %v", err)
		}
	})

	t.Run("manifest item lookup error degrades gracefully", func(t *testing.T) {
		carnage := &fakeCarnageGateway{
			report: CarnageReport{
				Entries: []CarnageEntry{
					{
						CharacterID: "char-1",
						EmblemHash:  9001,
						Weapons:     []CarnageWeapon{{ReferenceID: 1001}},
					},
				},
			},
		}
		manifest := &fakeManifestLookup{itemsErr: errors.New("firestore timeout")}
		svc := NewService(carnage, manifest, &fakeAggregateReader{found: false}, &fakeSnapshotReader{}, &fakeUserLookup{})
		report, err := svc.GetActivity(context.Background(), "12345")
		if err != nil {
			t.Fatalf("expected graceful degradation on manifest failure, got %v", err)
		}
		if len(report.Participants) != 1 {
			t.Fatalf("expected 1 participant, got %d", len(report.Participants))
		}
	})
}

func TestGetActivity_BravoWinsAndHighScoreDNFSortsBehindCompletedTeammates(t *testing.T) {
	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Teams: []Team{
				{ID: "18", Standing: "Defeat", Score: "80", TeamName: "Alpha"},
				{ID: "19", Standing: "Victory", Score: "150", TeamName: "Bravo"},
			},
			Entries: []CarnageEntry{
				{
					CharacterID: "alpha-dnf-higher",
					DisplayName: "AlphaQuitter",
					Standing:    1,
					HasStanding: true,
					Score:       stat(40, "40"),
					Completed:   false,
					PlayerStats: PlayerStats{
						Kills: stat(35, "35"),
						Team:  stat(18, "18"),
					},
				},
				{
					CharacterID: "alpha-top",
					DisplayName: "AlphaFinisher",
					Standing:    1,
					HasStanding: true,
					Score:       stat(25, "25"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(20, "20"),
						Team:  stat(18, "18"),
					},
				},
				{
					CharacterID: "bravo-dnf-high",
					DisplayName: "BravoQuitter",
					Standing:    0,
					HasStanding: true,
					Score:       stat(30, "30"),
					Completed:   false,
					PlayerStats: PlayerStats{
						Kills: stat(25, "25"),
						Team:  stat(19, "19"),
					},
				},
				{
					CharacterID: "bravo-completed-low",
					DisplayName: "BravoSupport",
					Standing:    0,
					HasStanding: true,
					Score:       stat(10, "10"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(8, "8"),
						Team:  stat(19, "19"),
					},
				},
				{
					CharacterID: "bravo-completed-top",
					DisplayName: "BravoMVP",
					Standing:    0,
					HasStanding: true,
					Score:       stat(22, "22"),
					Completed:   true,
					PlayerStats: PlayerStats{
						Kills: stat(18, "18"),
						Team:  stat(19, "19"),
					},
				},
			},
		},
	}

	svc := NewService(carnage, &fakeManifestLookup{}, &fakeAggregateReader{found: false}, &fakeSnapshotReader{}, &fakeUserLookup{})
	report, err := svc.GetActivity(context.Background(), "777001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotOrder := make([]string, 0, len(report.Participants))
	for _, p := range report.Participants {
		gotOrder = append(gotOrder, p.CharacterID)
	}
	wantOrder := []string{
		"bravo-completed-top",
		"bravo-completed-low",
		"bravo-dnf-high",
		"alpha-top",
		"alpha-dnf-higher",
	}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("expected winning Bravo team first and DNFs behind completed teammates: got %v, want %v", gotOrder, wantOrder)
	}
}

func TestGetActivity_DetachedSnapshotLinkOmitsStaleConfidenceLevel(t *testing.T) {
	carnage := &fakeCarnageGateway{
		report: CarnageReport{
			Entries: []CarnageEntry{
				{
					CharacterID:  "char-detached",
					MembershipID: "mem-1",
					DisplayName:  "DetachedPlayer",
					Completed:    true,
				},
			},
		},
	}

	aggReader := &fakeAggregateReader{
		found: true,
		agg: Aggregate{
			ID:         "agg-detached",
			ActivityID: "777002",
			SnapshotLinks: map[string]SnapshotLink{
				"char-detached": {
					CharacterID:     "char-detached",
					SessionID:       "",
					SnapshotID:      "",
					ConfidenceLevel: ConfidenceHigh,
					UserID:          "user-1",
				},
			},
		},
	}

	userLookup := &fakeUserLookup{
		partyUsers: []User{{ID: "user-1", PrimaryMembershipID: "mem-1", DisplayName: "DetachedPlayer"}},
	}

	svc := NewService(carnage, &fakeManifestLookup{}, aggReader, &fakeSnapshotReader{}, userLookup)
	report, err := svc.GetActivity(context.Background(), "777002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(report.Participants))
	}
	p := report.Participants[0]
	if p.ConfidenceLevel != "" {
		t.Fatalf("expected empty ConfidenceLevel for detached SnapshotLink, got %q", p.ConfidenceLevel)
	}
	if p.SessionID != "" || p.SnapshotID != "" {
		t.Fatalf("expected empty SessionID/SnapshotID, got session=%q snapshot=%q", p.SessionID, p.SnapshotID)
	}
	if p.UserID != "user-1" {
		t.Fatalf("expected registered userId still populated, got %q", p.UserID)
	}
}
