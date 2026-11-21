package user

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"reflect"
	"strconv"
	"testing"

	clog "github.com/Shannor/canonical-logger"
)

type fakeD2SearchService struct {
	destiny.Service
	results      []api.SearchUserResult
	hasMore      bool
	err          error
	capturedPref string
	capturedPage int32
	calls        int
}

func (f *fakeD2SearchService) Search(_ context.Context, prefix string, page int32) ([]api.SearchUserResult, bool, error) {
	f.calls++
	f.capturedPref = prefix
	f.capturedPage = page
	if f.err != nil {
		return nil, false, f.err
	}
	return f.results, f.hasMore, nil
}

type fakeUserSearchStore struct {
	prefixUsers       []User
	prefixErr         error
	prefixCalls       int
	crossRefUsers     []User
	crossRefErr       error
	crossRefCalls     int
	capturedBungieIDs []string
	capturedPrimIDs   []string
}

func (f *fakeUserSearchStore) findUsersByPrefix(_ context.Context, _ string, _ string) ([]User, error) {
	f.prefixCalls++
	if f.prefixErr != nil {
		return nil, f.prefixErr
	}
	return f.prefixUsers, nil
}

func (f *fakeUserSearchStore) findUsersByMembershipIDs(_ context.Context, bungieNetIDs []string, primaryMembershipIDs []string) ([]User, error) {
	f.crossRefCalls++
	f.capturedBungieIDs = append([]string(nil), bungieNetIDs...)
	f.capturedPrimIDs = append([]string(nil), primaryMembershipIDs...)
	if f.crossRefErr != nil {
		return nil, f.crossRefErr
	}
	return f.crossRefUsers, nil
}

func TestUserPrimaryMembership(t *testing.T) {
	t.Run("matches primary membership type", func(t *testing.T) {
		u := &User{
			PrimaryMembershipID: "4611686018467484863",
			Memberships: []Membership{
				{ID: "111", Type: 1},
				{ID: "4611686018467484863", Type: 3},
			},
		}
		pmID, mType, err := u.PrimaryMembership()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pmID != 4611686018467484863 || mType != 3 {
			t.Fatalf("expected (4611686018467484863, 3), got (%d, %d)", pmID, mType)
		}
	})

	t.Run("falls back to first membership type when primary ID not in list", func(t *testing.T) {
		u := &User{
			PrimaryMembershipID: "4611686018467484863",
			Memberships: []Membership{
				{ID: "111", Type: 2},
			},
		}
		pmID, mType, err := u.PrimaryMembership()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pmID != 4611686018467484863 || mType != 2 {
			t.Fatalf("expected (4611686018467484863, 2), got (%d, %d)", pmID, mType)
		}
	})

	t.Run("errors on nil user, empty/non-numeric primary membership ID, or missing membership type", func(t *testing.T) {
		var nilUser *User
		if _, _, err := nilUser.PrimaryMembership(); err == nil {
			t.Fatal("expected error for nil user")
		}
		if _, _, err := (&User{}).PrimaryMembership(); err == nil {
			t.Fatal("expected error for empty primary membership ID")
		}
		if _, _, err := (&User{PrimaryMembershipID: "not-a-number", Memberships: []Membership{{ID: "not-a-number", Type: 3}}}).PrimaryMembership(); err == nil {
			t.Fatal("expected error for non-numeric primary membership ID")
		}
		if _, _, err := (&User{PrimaryMembershipID: "4611686018467484863"}).PrimaryMembership(); err == nil {
			t.Fatal("expected error when memberships list is empty")
		}
		if _, _, err := (&User{PrimaryMembershipID: "4611686018467484863", Memberships: []Membership{{ID: "4611686018467484863", Type: 0}}}).PrimaryMembership(); err == nil {
			t.Fatal("expected error when membership type is 0")
		}
	})
}

func TestUserServiceSearch(t *testing.T) {
	hunter := api.Character{Id: "char-1", Class: "Hunter", Light: 2010}

	t.Run("recorded Firestore-only match returns isRecorded=true, userId, and characters", func(t *testing.T) {
		store := &fakeUserSearchStore{
			prefixUsers: []User{
				{
					ID:                  "user-1",
					MemberID:            "bnet-1",
					PrimaryMembershipID: "pm-1",
					DisplayName:         "Weegee",
					UniqueName:          "Weegee#1234",
					Memberships: []Membership{
						{ID: "pm-1", Type: 3, DisplayName: "WeegeeSteam"},
					},
					Characters: []api.Character{hunter},
				},
			},
		}
		d2 := &fakeD2SearchService{results: []api.SearchUserResult{}, hasMore: false}
		svc := &userService{d2Service: d2, store: store}

		results, hasMore, err := svc.Search(context.Background(), "Weegee", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasMore {
			t.Errorf("expected hasMore=false")
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		got := results[0]
		if !got.IsRecorded {
			t.Errorf("expected IsRecorded=true")
		}
		if got.UserID == nil || *got.UserID != "user-1" {
			t.Errorf("expected UserID=user-1, got %v", got.UserID)
		}
		if got.Characters == nil || len(*got.Characters) != 1 || (*got.Characters)[0].Id != "char-1" {
			t.Errorf("expected 1 character char-1, got %v", got.Characters)
		}
		if got.PrimaryMembershipType == nil || *got.PrimaryMembershipType != 3 {
			t.Errorf("expected PrimaryMembershipType=3, got %v", got.PrimaryMembershipType)
		}
		if got.BungieNetMembershipID == nil || *got.BungieNetMembershipID != "bnet-1" {
			t.Errorf("expected BungieNetMembershipID=bnet-1, got %v", got.BungieNetMembershipID)
		}
	})

	t.Run("unrecorded Bungie-only match returns isRecorded=false and userId=nil", func(t *testing.T) {
		store := &fakeUserSearchStore{}
		d2 := &fakeD2SearchService{
			hasMore: true,
			results: []api.SearchUserResult{
				{
					IsRecorded:            false,
					BungieNetMembershipID: ptr.Of("bnet-99"),
					PrimaryMembershipID:   "pm-99",
					PrimaryMembershipType: ptr.Of(int64(2)),
					DisplayName:           "RandomGuardian",
					UniqueName:            "RandomGuardian#9999",
					IconPath:              ptr.Of("/icons/psn.png"),
					AlternateNames:        []string{"RandomPSN"},
				},
			},
		}
		svc := &userService{d2Service: d2, store: store}

		results, hasMore, err := svc.Search(context.Background(), "Random", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasMore {
			t.Errorf("expected hasMore=true")
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		got := results[0]
		if got.IsRecorded {
			t.Errorf("expected IsRecorded=false")
		}
		if got.UserID != nil {
			t.Errorf("expected UserID=nil, got %v", *got.UserID)
		}
		if got.Characters != nil {
			t.Errorf("expected Characters=nil, got %v", *got.Characters)
		}
	})

	t.Run("Bungie hit cross-referenced with recorded Firestore user is upgraded, merged, deduplicated, and ranked first", func(t *testing.T) {
		recordedUser := User{
			ID:                  "user-recorded",
			MemberID:            "bnet-rec",
			PrimaryMembershipID: "pm-rec",
			DisplayName:         "Guardian",
			UniqueName:          "Guardian",
			Memberships: []Membership{
				{ID: "pm-rec", Type: 3, DisplayName: "GuardianOldSteam"},
			},
			Characters: []api.Character{hunter},
		}
		store := &fakeUserSearchStore{
			prefixUsers:   []User{recordedUser},
			crossRefUsers: []User{recordedUser},
		}
		d2 := &fakeD2SearchService{
			hasMore: true,
			results: []api.SearchUserResult{
				{
					IsRecorded:            false,
					BungieNetMembershipID: ptr.Of("bnet-unrec-1"),
					PrimaryMembershipID:   "pm-unrec-1",
					DisplayName:           "GuardianAlpha",
					UniqueName:            "GuardianAlpha#1111",
					AlternateNames:        []string{"Alpha"},
				},
				{
					IsRecorded:            false,
					BungieNetMembershipID: ptr.Of("bnet-rec"),
					PrimaryMembershipID:   "pm-rec",
					PrimaryMembershipType: ptr.Of(int64(3)),
					DisplayName:           "Guardian",
					UniqueName:            "Guardian#4321",
					IconPath:              ptr.Of("/icons/steam.png"),
					AlternateNames:        []string{"GuardianSteam"},
				},
			},
		}
		svc := &userService{d2Service: d2, store: store}

		results, hasMore, err := svc.Search(context.Background(), "Guardian", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasMore {
			t.Errorf("expected hasMore=true")
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 deduplicated results, got %d: %+v", len(results), results)
		}

		first := results[0]
		if !first.IsRecorded {
			t.Fatalf("expected recorded user ranked first, got %+v", first)
		}
		if first.UserID == nil || *first.UserID != "user-recorded" {
			t.Errorf("expected UserID=user-recorded, got %v", first.UserID)
		}
		if first.Characters == nil || len(*first.Characters) != 1 {
			t.Errorf("expected Characters populated, got %v", first.Characters)
		}
		if first.UniqueName != "Guardian#4321" {
			t.Errorf("expected merged UniqueName with discriminator Guardian#4321, got %q", first.UniqueName)
		}
		if first.IconPath == nil || *first.IconPath != "/icons/steam.png" {
			t.Errorf("expected merged IconPath, got %v", first.IconPath)
		}
		wantAlt := []string{"GuardianSteam", "GuardianOldSteam"}
		if !reflect.DeepEqual(first.AlternateNames, wantAlt) {
			t.Errorf("expected merged AlternateNames=%v, got %v", wantAlt, first.AlternateNames)
		}
		if results[1].IsRecorded || results[1].PrimaryMembershipID != "pm-unrec-1" {
			t.Errorf("expected unrecorded Bungie hit second, got %+v", results[1])
		}
	})

	t.Run("page > 0 skips local prefix query but still cross-references Bungie hits", func(t *testing.T) {
		store := &fakeUserSearchStore{
			crossRefUsers: []User{
				{
					ID:                  "user-page1",
					MemberID:            "bnet-p1",
					PrimaryMembershipID: "pm-p1",
					DisplayName:         "PageOneHero",
					UniqueName:          "PageOneHero#7777",
					Characters:          []api.Character{hunter},
				},
			},
		}
		d2 := &fakeD2SearchService{
			hasMore: false,
			results: []api.SearchUserResult{
				{
					IsRecorded:          false,
					PrimaryMembershipID: "pm-p1",
					DisplayName:         "PageOneHero",
					UniqueName:          "PageOneHero#7777",
					AlternateNames:      []string{},
				},
			},
		}
		svc := &userService{d2Service: d2, store: store}

		results, hasMore, err := svc.Search(context.Background(), "PageOne", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasMore {
			t.Errorf("expected hasMore=false")
		}
		if store.prefixCalls != 0 {
			t.Errorf("expected 0 local prefix calls on page 1, got %d", store.prefixCalls)
		}
		if store.crossRefCalls != 1 {
			t.Errorf("expected 1 cross-reference call on page 1, got %d", store.crossRefCalls)
		}
		if len(results) != 1 || !results[0].IsRecorded || results[0].UserID == nil || *results[0].UserID != "user-page1" {
			t.Fatalf("expected page 1 Bungie hit upgraded to recorded user, got %+v", results)
		}
	})

	t.Run("deterministic ranking across all tiers and preserves Bungie order for unrecorded hits", func(t *testing.T) {
		store := &fakeUserSearchStore{
			prefixUsers: []User{
				{ID: "u-rec-other", PrimaryMembershipID: "pm-3", DisplayName: "ApexZero", UniqueName: "ApexZero#3333"},
				{ID: "u-rec-display", PrimaryMembershipID: "pm-2", DisplayName: "Apex", UniqueName: "Apex#2222"},
				{ID: "u-rec-unique", PrimaryMembershipID: "pm-1", DisplayName: "ApexRec", UniqueName: "Apex#1234"},
			},
		}
		d2 := &fakeD2SearchService{
			results: []api.SearchUserResult{
				// Unrecorded results in Bungie relevance order: Zeta first, then Alpha.
				{IsRecorded: false, PrimaryMembershipID: "pm-u1", DisplayName: "ApexZeta", UniqueName: "ApexZeta#8888", AlternateNames: []string{}},
				{IsRecorded: false, PrimaryMembershipID: "pm-u2", DisplayName: "ApexAlpha", UniqueName: "ApexAlpha#9999", AlternateNames: []string{}},
				{IsRecorded: false, PrimaryMembershipID: "pm-u-exact", DisplayName: "ApexUnrec", UniqueName: "apex#1234", AlternateNames: []string{}},
			},
		}
		svc := &userService{d2Service: d2, store: store}

		// Case 1: query matches exact uniqueName -> recorded exact uniqueName first, then unrecorded exact uniqueName, then remaining recorded, then unrecorded in Bungie order.
		results, _, err := svc.Search(context.Background(), "APEX#1234", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotIDs := make([]string, len(results))
		for i, r := range results {
			gotIDs[i] = r.PrimaryMembershipID
		}
		wantIDs := []string{"pm-1", "pm-u-exact", "pm-2", "pm-3", "pm-u1", "pm-u2"}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("expected ranking order %v, got %v", wantIDs, gotIDs)
		}

		// Case 2: query matches exact displayName -> recorded exact displayName beats other recorded users, unrecorded stays in Bungie relevance order.
		results2, _, err := svc.Search(context.Background(), "apex", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotIDs2 := make([]string, len(results2))
		for i, r := range results2 {
			gotIDs2[i] = r.PrimaryMembershipID
		}
		wantIDs2 := []string{"pm-2", "pm-1", "pm-3", "pm-u1", "pm-u2", "pm-u-exact"}
		if !reflect.DeepEqual(gotIDs2, wantIDs2) {
			t.Fatalf("expected displayName ranking order %v, got %v", wantIDs2, gotIDs2)
		}
	})

	t.Run("graceful degradation returns Firestore results and hasMore=false when Bungie fails", func(t *testing.T) {
		store := &fakeUserSearchStore{
			prefixUsers: []User{
				{
					ID:                  "user-local",
					PrimaryMembershipID: "pm-local",
					DisplayName:         "Weegee",
					UniqueName:          "Weegee#1234",
					Characters:          []api.Character{hunter},
				},
			},
		}
		d2 := &fakeD2SearchService{
			err: destiny.ErrDestinyServerDown,
		}
		svc := &userService{d2Service: d2, store: store}

		ctx := clog.NewContext(context.Background())
		results, hasMore, err := svc.Search(ctx, "Weegee", 0)
		if err != nil {
			t.Fatalf("expected graceful fallback without error, got %v", err)
		}
		if hasMore {
			t.Errorf("expected hasMore=false when Bungie is degraded")
		}
		if len(results) != 1 || !results[0].IsRecorded || results[0].UserID == nil || *results[0].UserID != "user-local" {
			t.Fatalf("expected local recorded user returned on Bungie failure, got %+v", results)
		}
		if store.crossRefCalls != 0 {
			t.Errorf("expected 0 cross-ref calls when Bungie failed, got %d", store.crossRefCalls)
		}
	})

	t.Run("propagates Firestore error when local prefix search fails", func(t *testing.T) {
		store := &fakeUserSearchStore{prefixErr: errors.New("firestore unavailable")}
		d2 := &fakeD2SearchService{}
		svc := &userService{d2Service: d2, store: store}

		_, _, err := svc.Search(context.Background(), "Weegee", 0)
		if err == nil {
			t.Fatal("expected error when Firestore fails")
		}
	})
}

func TestChunkUniqueStrings(t *testing.T) {
	input := make([]string, 0, 70)
	for i := 0; i < 65; i++ {
		input = append(input, "id-"+strconv.Itoa(i))
	}
	// Add duplicates and empty strings.
	input = append(input, "", "   ", "id-0", "id-10", "id-64")

	chunks := chunkUniqueStrings(input, firestoreInBatchLimit)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks (30, 30, 5), got %d", len(chunks))
	}
	if len(chunks[0]) != 30 || len(chunks[1]) != 30 || len(chunks[2]) != 5 {
		t.Fatalf("unexpected chunk sizes: %d, %d, %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

