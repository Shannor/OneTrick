package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/logger"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"oneTrick/validator"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

type mockAuthService struct {
	destiny.AuthService
	membershipData *bungie.MembershipData
}

func (m *mockAuthService) GetAccessToken(ctx context.Context, code string) (*destiny.AuthResponse, error) {
	return &destiny.AuthResponse{
		AccessToken:  "test-token",
		MembershipID: "123456",
		ExpiresIn:    3600,
	}, nil
}

func (m *mockAuthService) GetCurrentUser(ctx context.Context, token string) (*bungie.MembershipData, error) {
	return m.membershipData, nil
}

type mockUserService struct {
	user.Service
	existingUser          *user.User
	createdUser           *user.User
	updatedCharacters     []api.Character
	updateCalls           atomic.Int32
	updateSearchFieldCall atomic.Int32
	searchResults         []api.SearchUserResult
	searchHasMore         bool
	searchErr             error
	capturedSearchQuery   string
	capturedSearchPage    int
}

func (m *mockUserService) GetUser(ctx context.Context, id string) (*user.User, error) {
	if m.existingUser != nil {
		return m.existingUser, nil
	}
	return nil, user.NotFound
}

func (m *mockUserService) CreateUser(ctx context.Context, u *user.User) (*user.User, error) {
	u.ID = "created-id"
	m.createdUser = u
	return u, nil
}

func (m *mockUserService) UpdateCharacters(ctx context.Context, userID string, characters []api.Character) error {
	m.updateCalls.Add(1)
	m.updatedCharacters = characters
	return nil
}

func (m *mockUserService) UpdateSearchFields(ctx context.Context, userID string, displayName string, uniqueName string) error {
	m.updateSearchFieldCall.Add(1)
	return nil
}

func (m *mockUserService) Search(ctx context.Context, query string, page int) ([]api.SearchUserResult, bool, error) {
	m.capturedSearchQuery = query
	m.capturedSearchPage = page
	if m.searchErr != nil {
		return nil, false, m.searchErr
	}
	return m.searchResults, m.searchHasMore, nil
}

type mockD2Service struct {
	destiny.Service
	capturedPrimaryID      int64
	capturedMembershipType int64
}

func (m *mockD2Service) GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error) {
	m.capturedPrimaryID = primaryMembershipId
	m.capturedMembershipType = membershipType
	return []api.Character{
		{Id: "char1", Class: "Titan", Light: 2000},
	}, nil
}

func TestLoginNewUserCrossSave(t *testing.T) {
	primaryID := "4611686018499886480"
	steamID := "4611686018499886480"
	steamType := int32(3)
	xboxID := "12345"
	xboxType := int32(1)
	displayName := "TestGuardian"
	bungieNetID := "9999"

	memberships := []bungie.GroupsV2GroupUserInfoCard{
		{
			MembershipId:   &xboxID,
			MembershipType: &xboxType,
			DisplayName:    &displayName,
		},
		{
			MembershipId:   &steamID,
			MembershipType: &steamType,
			DisplayName:    &displayName,
		},
	}

	authMock := &mockAuthService{
		membershipData: &bungie.MembershipData{
			PrimaryMembershipId: &primaryID,
			DestinyMemberships:  &memberships,
			BungieNetUser: &bungie.UserGeneralUser{
				MembershipId: &bungieNetID,
				DisplayName:  &displayName,
			},
		},
	}

	userMock := &mockUserService{}
	d2Mock := &mockD2Service{}

	server := Server{
		D2AuthService: authMock,
		UserService:   userMock,
		D2Service:     d2Mock,
	}

	req := api.LoginRequestObject{
		Body: &api.LoginJSONRequestBody{
			Code: "valid-code",
		},
	}

	resp, err := server.Login(context.Background(), req)
	if err != nil {
		t.Fatalf("expected login to succeed, got: %v", err)
	}

	if _, ok := resp.(api.Login200JSONResponse); !ok {
		t.Fatalf("expected Login200JSONResponse, got: %T", resp)
	}

	if d2Mock.capturedPrimaryID != 4611686018499886480 {
		t.Errorf("expected primaryMembershipId 4611686018499886480, got %d", d2Mock.capturedPrimaryID)
	}

	if d2Mock.capturedMembershipType != 3 {
		t.Errorf("expected membershipType 3 (Steam), got %d", d2Mock.capturedMembershipType)
	}

	if userMock.createdUser == nil {
		t.Fatal("expected user to be created")
	}

	if userMock.createdUser.PrimaryMembershipID != primaryID {
		t.Errorf("expected created user primary membership ID %s, got %s", primaryID, userMock.createdUser.PrimaryMembershipID)
	}
}

func TestLoginNewUserNoCrossSave(t *testing.T) {
	xboxID := "12345"
	xboxType := int32(1)
	displayName := "TestGuardian"
	bungieNetID := "9999"

	memberships := []bungie.GroupsV2GroupUserInfoCard{
		{
			MembershipId:   &xboxID,
			MembershipType: &xboxType,
			DisplayName:    &displayName,
		},
	}

	authMock := &mockAuthService{
		membershipData: &bungie.MembershipData{
			PrimaryMembershipId: nil,
			DestinyMemberships:  &memberships,
			BungieNetUser: &bungie.UserGeneralUser{
				MembershipId: &bungieNetID,
				DisplayName:  &displayName,
			},
		},
	}

	userMock := &mockUserService{}
	d2Mock := &mockD2Service{}

	server := Server{
		D2AuthService: authMock,
		UserService:   userMock,
		D2Service:     d2Mock,
	}

	req := api.LoginRequestObject{
		Body: &api.LoginJSONRequestBody{
			Code: "valid-code",
		},
	}

	resp, err := server.Login(context.Background(), req)
	if err != nil {
		t.Fatalf("expected login to succeed, got: %v", err)
	}

	if _, ok := resp.(api.Login200JSONResponse); !ok {
		t.Fatalf("expected Login200JSONResponse, got: %T", resp)
	}

	if d2Mock.capturedPrimaryID != 12345 {
		t.Errorf("expected primaryMembershipId 12345, got %d", d2Mock.capturedPrimaryID)
	}

	if d2Mock.capturedMembershipType != 1 {
		t.Errorf("expected membershipType 1 (Xbox), got %d", d2Mock.capturedMembershipType)
	}
}

type mockSessionService struct {
	session.Service
	capturedCharID *string
}

func (m *mockSessionService) GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus, count int, offset int) ([]api.Session, error) {
	m.capturedCharID = characterID
	return []api.Session{
		{ID: "session1", UserID: *userID},
	}, nil
}

func TestGetUserSessionsEmptyCharacterID(t *testing.T) {
	sessionMock := &mockSessionService{}
	server := Server{
		SessionService: sessionMock,
	}

	req := api.GetUserSessionsRequestObject{
		UserID: "test-user-id",
		Params: api.GetUserSessionsParams{
			CharacterID: "", // Omitted / empty string in query params
		},
	}

	resp, err := server.GetUserSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("expected GetUserSessions to succeed, got: %v", err)
	}

	if _, ok := resp.(api.GetUserSessions200JSONResponse); !ok {
		t.Fatalf("expected GetUserSessions200JSONResponse, got: %T", resp)
	}

	if sessionMock.capturedCharID != nil {
		t.Errorf("expected capturedCharID to be nil when CharacterID parameter is empty, got %v", *sessionMock.capturedCharID)
	}
}

type mockStatsService struct {
	stats.Service
	capturedCount      int
	capturedOffset     int
	capturedGameMode   *api.GameMode
	capturedHasSimilar bool
	similarCountsResp  *map[string]int
	similarTarget      *api.CharacterSnapshot
	similarItems       []api.SimilarSnapshotItem
	similarStats       *map[string]api.PlayerStats
	similarCountsMap   *map[string]int
	similarErr         error
}

func (m *mockStatsService) GetSnapshotsWithMetrics(
	ctx context.Context,
	userID string,
	characterID string,
	gameMode *api.GameMode,
	minimumGames int,
	sortBy string,
	includeStats bool,
	hasSimilar bool,
	count int,
	offset int,
) ([]api.CharacterSnapshot, *map[string]api.PlayerStats, *map[string]int, *map[string]int, error) {
	m.capturedCount = count
	m.capturedOffset = offset
	m.capturedGameMode = gameMode
	m.capturedHasSimilar = hasSimilar
	return []api.CharacterSnapshot{}, nil, nil, m.similarCountsResp, nil
}

func (m *mockStatsService) GetSimilarSnapshots(
	ctx context.Context,
	snapshotID string,
) (*api.CharacterSnapshot, []api.SimilarSnapshotItem, *map[string]api.PlayerStats, *map[string]int, error) {
	if m.similarErr != nil {
		return nil, nil, nil, nil, m.similarErr
	}
	if m.similarTarget != nil {
		return m.similarTarget, m.similarItems, m.similarStats, m.similarCountsMap, nil
	}
	return &api.CharacterSnapshot{ID: snapshotID, Name: "Trials Hawkmoon", Loadout: api.Loadout{}}, []api.SimilarSnapshotItem{}, nil, nil, nil
}

func (m *mockStatsService) GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error) {
	return []api.FeaturedLoadout{
		{
			FeaturedReason: new("Top Hunter PvP Loadout of the Day"),
			UsageCount:     new(5),
			Snapshot: api.CharacterSnapshot{
				ID:   "snap-1",
				Name: "Featured Hunter Loadout",
			},
		},
	}, nil
}

func TestGetFeaturedLoadouts(t *testing.T) {
	statsMock := &mockStatsService{}
	server := Server{
		StatsService: statsMock,
	}

	req := api.GetFeaturedLoadoutsRequestObject{
		Params: api.GetFeaturedLoadoutsParams{
			Count: new(5),
		},
	}

	resp, err := server.GetFeaturedLoadouts(context.Background(), req)
	if err != nil {
		t.Fatalf("expected GetFeaturedLoadouts to succeed, got: %v", err)
	}

	jsonResp, ok := resp.(api.GetFeaturedLoadouts200JSONResponse)
	if !ok {
		t.Fatalf("expected GetFeaturedLoadouts200JSONResponse, got: %T", resp)
	}

	if len(jsonResp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(jsonResp.Items))
	}

	if jsonResp.Items[0].Snapshot.ID != "snap-1" {
		t.Errorf("expected snapshot ID snap-1, got %s", jsonResp.Items[0].Snapshot.ID)
	}
}

func TestGetSnapshotsZeroIndexedPageOffset(t *testing.T) {
	tests := []struct {
		name           string
		page           *int
		count          *int
		expectedOffset int
	}{
		{name: "nil page defaults to offset 0", page: nil, count: new(10), expectedOffset: 0},
		{name: "page 0 uses offset 0", page: new(0), count: new(10), expectedOffset: 0},
		{name: "page 1 uses offset 10", page: new(1), count: new(10), expectedOffset: 10},
		{name: "page 2 uses offset 20", page: new(2), count: new(10), expectedOffset: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statsMock := &mockStatsService{}
			d2Mock := &mockD2Service{}
			server := Server{
				StatsService: statsMock,
				D2Service:    d2Mock,
			}

			req := api.GetSnapshotsRequestObject{
				Params: api.GetSnapshotsParams{
					UserID:      "u1",
					CharacterID: "c1",
					Page:        tt.page,
					Count:       tt.count,
				},
			}

			_, err := server.GetSnapshots(context.Background(), req)
			if err != nil {
				t.Fatalf("expected GetSnapshots to succeed, got: %v", err)
			}
			if statsMock.capturedOffset != tt.expectedOffset {
				t.Errorf("expected offset %d, got %d", tt.expectedOffset, statsMock.capturedOffset)
			}
		})
	}
}

type mockSessionGetService struct {
	session.Service
	ses *api.Session
}

func (m *mockSessionGetService) Get(ctx context.Context, id string) (*api.Session, error) {
	return m.ses, nil
}

type mockAggregateGetService struct {
	aggregate.Service
	aggs []api.Aggregate
}

func (m *mockAggregateGetService) GetAggregates(ctx context.Context, ids []string) ([]api.Aggregate, error) {
	return m.aggs, nil
}

type mockSnapshotGetByIDsService struct {
	snapshot.Service
	capturedIDs []string
}

func (m *mockSnapshotGetByIDsService) GetByIDs(ctx context.Context, ids []string) ([]api.CharacterSnapshot, error) {
	m.capturedIDs = ids
	res := make([]api.CharacterSnapshot, 0, len(ids))
	for _, id := range ids {
		res = append(res, api.CharacterSnapshot{ID: id})
	}
	return res, nil
}

func TestGetSessionAggregatesDeduplicatesSnapshotIDs(t *testing.T) {
	const charID = "char-1"
	aggIDs := make([]string, 35)
	aggs := make([]api.Aggregate, 35)
	for i := 0; i < 35; i++ {
		aggIDs[i] = fmt.Sprintf("agg-%d", i)
		snapID := "snap-a"
		if i%2 == 1 {
			snapID = "snap-b"
		}
		aggs[i] = api.Aggregate{
			ID: aggIDs[i],
			SnapshotLinks: map[string]api.SnapshotLink{
				charID: {
					CharacterID: charID,
					SnapshotID:  new(snapID),
				},
			},
		}
	}

	snapMock := &mockSnapshotGetByIDsService{}
	server := Server{
		SessionService: &mockSessionGetService{
			ses: &api.Session{
				ID:           "ses-1",
				CharacterID:  charID,
				AggregateIDs: aggIDs,
			},
		},
		AggregateService: &mockAggregateGetService{aggs: aggs},
		SnapshotService:  snapMock,
	}

	resp, err := server.GetSessionAggregates(context.Background(), api.GetSessionAggregatesRequestObject{
		SessionId: "ses-1",
	})
	if err != nil {
		t.Fatalf("expected GetSessionAggregates to succeed, got: %v", err)
	}
	jsonResp, ok := resp.(api.GetSessionAggregates200JSONResponse)
	if !ok {
		t.Fatalf("expected GetSessionAggregates200JSONResponse, got: %T", resp)
	}
	if len(snapMock.capturedIDs) != 2 {
		t.Fatalf("expected 2 deduplicated snapshot IDs passed to GetByIDs, got %d (%v)", len(snapMock.capturedIDs), snapMock.capturedIDs)
	}
	if len(jsonResp.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots in response map, got %d", len(jsonResp.Snapshots))
	}
}

type mockSnapshotMergeAndGetService struct {
	snapshot.Service
	snap            *api.CharacterSnapshot
	getErr          error
	mergeErr        error
	capturedUserID  string
	capturedTarget  string
	capturedSources []string
}

func (m *mockSnapshotMergeAndGetService) Get(ctx context.Context, id string) (*api.CharacterSnapshot, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.snap, nil
}

func (m *mockSnapshotMergeAndGetService) Merge(ctx context.Context, userID string, targetID string, sourceIDs []string) (api.CharacterSnapshot, error) {
	m.capturedUserID = userID
	m.capturedTarget = targetID
	m.capturedSources = sourceIDs
	if m.mergeErr != nil {
		return api.CharacterSnapshot{}, m.mergeErr
	}
	return api.CharacterSnapshot{ID: targetID}, nil
}

func TestOpenAPISpecLoadsAndValidates(t *testing.T) {
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("expected embedded swagger spec to load cleanly, got: %v", err)
	}
	if err := swagger.Validate(context.Background()); err != nil {
		t.Fatalf("expected bundled swagger spec to pass kin-openapi validation, got: %v", err)
	}
	if swagger.Paths.Find("/snapshots/{snapshotId}/similar") == nil {
		t.Fatal("expected /snapshots/{snapshotId}/similar path in swagger spec")
	}
}

func TestMergeSnapshotsSingleAndBatchRequestValidation(t *testing.T) {
	t.Run("rejects empty source IDs with 400", func(t *testing.T) {
		server := Server{SnapshotService: &mockSnapshotMergeAndGetService{}}
		resp, err := server.MergeSnapshots(context.Background(), api.MergeSnapshotsRequestObject{
			SnapshotID: "target-1",
			Params:     api.MergeSnapshotsParams{XUserID: "user-1"},
			Body:       &api.MergeSnapshotsJSONRequestBody{},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := resp.(api.MergeSnapshots400JSONResponse); !ok {
			t.Fatalf("expected MergeSnapshots400JSONResponse, got %T", resp)
		}
	})

	t.Run("rejects self-merge in batch with 400", func(t *testing.T) {
		server := Server{SnapshotService: &mockSnapshotMergeAndGetService{}}
		resp, err := server.MergeSnapshots(context.Background(), api.MergeSnapshotsRequestObject{
			SnapshotID: "target-1",
			Params:     api.MergeSnapshotsParams{XUserID: "user-1"},
			Body: &api.MergeSnapshotsJSONRequestBody{
				SourceSnapshotIDs: new([]string{"src-1", "target-1"}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := resp.(api.MergeSnapshots400JSONResponse); !ok {
			t.Fatalf("expected MergeSnapshots400JSONResponse, got %T", resp)
		}
	})

	t.Run("deduplicates single and batch sourceSnapshotIds and forwards X-User-ID", func(t *testing.T) {
		mockSnap := &mockSnapshotMergeAndGetService{}
		server := Server{SnapshotService: mockSnap}
		resp, err := server.MergeSnapshots(context.Background(), api.MergeSnapshotsRequestObject{
			SnapshotID: "target-1",
			Params:     api.MergeSnapshotsParams{XUserID: "user-1"},
			Body: &api.MergeSnapshotsJSONRequestBody{
				SourceSnapshotID:  new("src-1"),
				SourceSnapshotIDs: new([]string{"src-1", "src-2"}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := resp.(api.MergeSnapshots200JSONResponse); !ok {
			t.Fatalf("expected MergeSnapshots200JSONResponse, got %T", resp)
		}
		if mockSnap.capturedUserID != "user-1" || mockSnap.capturedTarget != "target-1" {
			t.Fatalf("unexpected user/target passed to Merge: user=%q target=%q", mockSnap.capturedUserID, mockSnap.capturedTarget)
		}
		if len(mockSnap.capturedSources) != 2 || mockSnap.capturedSources[0] != "src-1" || mockSnap.capturedSources[1] != "src-2" {
			t.Fatalf("expected [src-1 src-2], got %v", mockSnap.capturedSources)
		}
	})

	t.Run("maps InvalidMerge->400, Unauthorized->401, NotFound->404", func(t *testing.T) {
		cases := []struct {
			err      error
			checkTyp func(api.MergeSnapshotsResponseObject) bool
			label    string
		}{
			{
				err: fmt.Errorf("%w: incompatible subclass", snapshot.InvalidMerge),
				checkTyp: func(r api.MergeSnapshotsResponseObject) bool {
					_, ok := r.(api.MergeSnapshots400JSONResponse)
					return ok
				},
				label: "400",
			},
			{
				err: snapshot.Unauthorized,
				checkTyp: func(r api.MergeSnapshotsResponseObject) bool {
					_, ok := r.(api.MergeSnapshots401JSONResponse)
					return ok
				},
				label: "401",
			},
			{
				err: snapshot.NotFound,
				checkTyp: func(r api.MergeSnapshotsResponseObject) bool {
					_, ok := r.(api.MergeSnapshots404JSONResponse)
					return ok
				},
				label: "404",
			},
		}

		for _, tc := range cases {
			server := Server{SnapshotService: &mockSnapshotMergeAndGetService{mergeErr: tc.err}}
			resp, err := server.MergeSnapshots(context.Background(), api.MergeSnapshotsRequestObject{
				SnapshotID: "target-1",
				Params:     api.MergeSnapshotsParams{XUserID: "user-1"},
				Body:       &api.MergeSnapshotsJSONRequestBody{SourceSnapshotID: new("src-1")},
			})
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.label, err)
			}
			if !tc.checkTyp(resp) {
				t.Fatalf("expected %s response for %v, got %T", tc.label, tc.err, resp)
			}
		}
	})
}

func TestGetSimilarSnapshotsAndHasSimilarHandler(t *testing.T) {
	t.Run("returns 404 when target not found", func(t *testing.T) {
		server := Server{StatsService: &mockStatsService{similarErr: snapshot.NotFound}}
		resp, err := server.GetSimilarSnapshots(context.Background(), api.GetSimilarSnapshotsRequestObject{
			SnapshotID: "missing",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := resp.(api.GetSimilarSnapshots404JSONResponse); !ok {
			t.Fatalf("expected GetSimilarSnapshots404JSONResponse, got %T", resp)
		}
	})

	t.Run("returns 200 with target, tiered items, stats, and counts", func(t *testing.T) {
		counts := map[string]int{"target-1": 12, "cand-1": 3}
		statsMap := map[string]api.PlayerStats{
			"target-1": {Kd: new(api.StatsValuePair{Value: new(1.50)})},
			"cand-1":   {Kd: new(api.StatsValuePair{Value: new(2.00)})},
		}
		diffs := map[string]int{"Recovery": 10}
		server := Server{
			StatsService: &mockStatsService{
				similarTarget: &api.CharacterSnapshot{ID: "target-1", Name: "Trials Hawkmoon", Loadout: api.Loadout{}},
				similarItems: []api.SimilarSnapshotItem{
					{
						Snapshot:         api.CharacterSnapshot{ID: "cand-1", Name: "Offshoot", Loadout: api.Loadout{}},
						Tier:             api.SimilarityTierHigh,
						ExoticArmorMatch: true,
						PowerWeaponMatch: false,
						StatDiffs:        &diffs,
					},
				},
				similarCountsMap: &counts,
				similarStats:     &statsMap,
			},
		}
		resp, err := server.GetSimilarSnapshots(context.Background(), api.GetSimilarSnapshotsRequestObject{
			SnapshotID: "target-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		okResp, ok := resp.(api.GetSimilarSnapshots200JSONResponse)
		if !ok {
			t.Fatalf("expected GetSimilarSnapshots200JSONResponse, got %T", resp)
		}
		if okResp.Target.ID != "target-1" || len(okResp.Items) != 1 || okResp.Items[0].Tier != api.SimilarityTierHigh {
			t.Fatalf("unexpected response payload: %+v", okResp)
		}
		if okResp.Count == nil || (*okResp.Count)["cand-1"] != 3 {
			t.Fatalf("expected candidate count 3, got %+v", okResp.Count)
		}
	})

	t.Run("GetSnapshots forwards hasSimilar and returns similarCounts", func(t *testing.T) {
		simCounts := map[string]int{"snap-a": 2}
		statsMock := &mockStatsService{similarCountsResp: &simCounts}
		server := Server{StatsService: statsMock}

		resp, err := server.GetSnapshots(context.Background(), api.GetSnapshotsRequestObject{
			Params: api.GetSnapshotsParams{
				UserID:      "u1",
				CharacterID: "c1",
				HasSimilar:  new(true),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !statsMock.capturedHasSimilar {
			t.Fatal("expected hasSimilar=true to be forwarded to StatsService")
		}
		okResp, ok := resp.(api.GetSnapshots200JSONResponse)
		if !ok {
			t.Fatalf("expected GetSnapshots200JSONResponse, got %T", resp)
		}
		if okResp.SimilarCounts == nil || (*okResp.SimilarCounts)["snap-a"] != 2 {
			t.Fatalf("expected similarCounts[snap-a]=2, got %+v", okResp.SimilarCounts)
		}
	})
}

func TestHTTPRouterOpenAPIMiddlewareEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger failed: %v", err)
	}
	swagger.Servers = nil

	snapMock := &mockSnapshotMergeAndGetService{
		snap: &api.CharacterSnapshot{ID: "target-1", Name: "Trials Hawkmoon", Loadout: api.Loadout{}},
	}
	statsMock := &mockStatsService{}
	d2Mock := &mockD2Service{}

	server := Server{
		SnapshotService: snapMock,
		StatsService:    statsMock,
		D2Service:       d2Mock,
	}

	r := gin.New()
	r.ContextWithFallback = true
	r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		ErrorHandler: logger.GinValidationErrorHandler,
		Options: openapi3filter.Options{
			AuthenticationFunc: validator.Authenticate,
		},
	}))
	h := api.NewStrictHandler(server, []api.StrictMiddlewareFunc{
		logger.GinStrictMiddleware[api.StrictHandlerFunc],
	})
	api.RegisterHandlersWithOptions(r, h, api.GinServerOptions{
		ErrorHandler: logger.GinParamErrorHandler,
	})

	t.Run("POST /snapshots/{snapshotId}/merge validates batch body and X-User-ID header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/snapshots/target-1/merge", strings.NewReader(`{"sourceSnapshotIds":["src-1","src-2"]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "user-1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("POST /snapshots/{snapshotId}/merge rejects missing X-User-ID header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/snapshots/target-1/merge", strings.NewReader(`{"sourceSnapshotId":"src-1"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("expected non-200 when required X-User-ID header is missing, got 200")
		}
	})

	t.Run("GET /snapshots/{snapshotId}/similar allows public read", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/snapshots/target-1/similar", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("GET /snapshots accepts hasSimilar query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/snapshots?userId=u1&characterId=c1&hasSimilar=true", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestGetUserCharacterRefreshAndFallback(t *testing.T) {
	t.Run("populates and persists characters synchronously when user has no characters", func(t *testing.T) {
		uMock := &mockUserService{
			existingUser: &user.User{
				ID:                  "u-empty",
				PrimaryMembershipID: "4611686018499886480",
				Memberships:         []user.Membership{{ID: "4611686018499886480", Type: 3}},
				Characters:          nil,
			},
		}
		d2Mock := &mockD2Service{}
		server := Server{UserService: uMock, D2Service: d2Mock}

		resp, err := server.GetUser(context.Background(), api.GetUserRequestObject{UserID: "u-empty"})
		if err != nil {
			t.Fatalf("expected GetUser to succeed, got %v", err)
		}
		okResp, ok := resp.(api.GetUser200JSONResponse)
		if !ok {
			t.Fatalf("expected GetUser200JSONResponse, got %T", resp)
		}
		if len(okResp.Characters) != 1 || okResp.Characters[0].Id != "char1" {
			t.Fatalf("expected synchronous fallback to populate response characters, got %+v", okResp.Characters)
		}
		if uMock.updateCalls.Load() != 1 || len(uMock.updatedCharacters) != 1 {
			t.Fatalf("expected synchronous fallback to persist characters once, got calls=%d", uMock.updateCalls.Load())
		}
	})

	t.Run("skips background refresh when characters were updated within 6h TTL", func(t *testing.T) {
		uMock := &mockUserService{
			existingUser: &user.User{
				ID:                    "u-fresh",
				PrimaryMembershipID:   "4611686018499886480",
				Memberships:           []user.Membership{{ID: "4611686018499886480", Type: 3}},
				Characters:            []api.Character{{Id: "char-existing", Class: "Hunter", Light: 2010}},
				LastUpdatedCharacters: time.Now().Add(-2 * time.Hour),
			},
		}
		d2Mock := &mockD2Service{}
		server := Server{UserService: uMock, D2Service: d2Mock}

		_, err := server.GetUser(context.Background(), api.GetUserRequestObject{UserID: "u-fresh"})
		if err != nil {
			t.Fatalf("expected GetUser to succeed, got %v", err)
		}
		time.Sleep(30 * time.Millisecond)
		if uMock.updateCalls.Load() != 0 {
			t.Fatalf("expected 0 background character updates within 6h TTL, got %d", uMock.updateCalls.Load())
		}
	})

	t.Run("deduplicates concurrent in-flight background refreshes", func(t *testing.T) {
		inFlightCharacterUpdates.Store("u-inflight", struct{}{})
		defer inFlightCharacterUpdates.Delete("u-inflight")

		uMock := &mockUserService{
			existingUser: &user.User{
				ID:                    "u-inflight",
				PrimaryMembershipID:   "4611686018499886480",
				Memberships:           []user.Membership{{ID: "4611686018499886480", Type: 3}},
				Characters:            []api.Character{{Id: "char-existing", Class: "Warlock", Light: 2010}},
				LastUpdatedCharacters: time.Now().Add(-12 * time.Hour),
			},
		}
		d2Mock := &mockD2Service{}
		server := Server{UserService: uMock, D2Service: d2Mock}

		_, err := server.GetUser(context.Background(), api.GetUserRequestObject{UserID: "u-inflight"})
		if err != nil {
			t.Fatalf("expected GetUser to succeed, got %v", err)
		}
		time.Sleep(30 * time.Millisecond)
		if uMock.updateCalls.Load() != 0 {
			t.Fatalf("expected in-flight guard to suppress duplicate background update, got %d calls", uMock.updateCalls.Load())
		}
	})
}

func TestGetSessionAggregatesEmptySession(t *testing.T) {
	server := Server{
		SessionService: &mockSessionGetService{
			ses: &api.Session{
				ID:           "ses-empty",
				CharacterID:  "char-1",
				AggregateIDs: nil,
			},
		},
	}

	resp, err := server.GetSessionAggregates(context.Background(), api.GetSessionAggregatesRequestObject{
		SessionId: "ses-empty",
	})
	if err != nil {
		t.Fatalf("expected nil error for session with no aggregates, got %v", err)
	}
	okResp, ok := resp.(api.GetSessionAggregates200JSONResponse)
	if !ok {
		t.Fatalf("expected GetSessionAggregates200JSONResponse, got %T", resp)
	}
	if len(okResp.Aggregates) != 0 || len(okResp.Snapshots) != 0 {
		t.Fatalf("expected empty aggregates and snapshots, got %+v", okResp)
	}
}

type mockCompSessionService struct {
	session.Service
	capturedTrackComp bool
	startedSession    *api.Session
	finalizeErrors    map[string]error
	finalizedIDs      []string
}

func (m *mockCompSessionService) Start(_ context.Context, userID string, characterID string, startedBy api.AuditField, trackComp bool) (*api.Session, error) {
	m.capturedTrackComp = trackComp
	if m.startedSession != nil {
		return m.startedSession, nil
	}
	return &api.Session{
		ID:           "ses-comp-1",
		UserID:       userID,
		CharacterID:  characterID,
		StartedAt:    time.Now(),
		AggregateIDs: []string{},
		StartedBy:    &startedBy,
	}, nil
}

func (m *mockCompSessionService) FinalizeCompTracking(_ context.Context, sessionID string) error {
	m.finalizedIDs = append(m.finalizedIDs, sessionID)
	if err, ok := m.finalizeErrors[sessionID]; ok {
		return err
	}
	return nil
}

func TestStartSessionAndFinalizeSessionsCompHandlers(t *testing.T) {
	uMock := &mockUserService{
		existingUser: &user.User{
			ID:          "user-1",
			DisplayName: "Zone 6 Weegee",
		},
	}

	t.Run("StartSession and StartUserSession forward trackComp=true", func(t *testing.T) {
		sesMock := &mockCompSessionService{
			startedSession: &api.Session{
				ID:           "ses-1",
				UserID:       "user-1",
				CharacterID:  "char-1",
				StartedAt:    time.Now(),
				AggregateIDs: []string{},
				CompTracking: &api.CompTracking{
					Enabled: true,
					Start: &api.CompRankSnapshot{
						Points:       5820,
						DivisionName: new("Platinum III"),
					},
				},
			},
		}
		server := Server{
			UserService:    uMock,
			SessionService: sesMock,
		}

		resp, err := server.StartSession(context.Background(), api.StartSessionRequestObject{
			Params: api.StartSessionParams{XUserID: "user-1", XMembershipID: "4611686018467484863"},
			Body: &api.StartSessionJSONRequestBody{
				UserID:      "user-1",
				CharacterID: "char-1",
				TrackComp:   new(true),
			},
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}
		if !sesMock.capturedTrackComp {
			t.Fatal("expected trackComp=true to be forwarded to SessionService.Start")
		}
		created, ok := resp.(api.StartSession201JSONResponse)
		if !ok || created.CompTracking == nil || !created.CompTracking.Enabled {
			t.Fatalf("expected 201 with CompTracking.Enabled=true, got %+v", resp)
		}

		sesMock.capturedTrackComp = false
		uResp, err := server.StartUserSession(context.Background(), api.StartUserSessionRequestObject{
			UserID: "user-1",
			Params: api.StartUserSessionParams{XUserID: "user-1", XMembershipID: "4611686018467484863"},
			Body: &api.StartUserSessionJSONRequestBody{
				UserID:      "user-1",
				CharacterID: "char-1",
				TrackComp:   new(true),
			},
		})
		if err != nil {
			t.Fatalf("StartUserSession failed: %v", err)
		}
		if !sesMock.capturedTrackComp {
			t.Fatal("expected trackComp=true to be forwarded in StartUserSession")
		}
		uCreated, ok := uResp.(api.StartUserSession201JSONResponse)
		if !ok || uCreated.CompTracking == nil || !uCreated.CompTracking.Enabled {
			t.Fatalf("expected 201 with CompTracking.Enabled=true, got %+v", uResp)
		}
	})

	t.Run("POST /internal/sessions/finalize-comp enforces X-Internal-API-Key, returns 204 on success, and 500 on batch failure", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		swagger, err := api.GetSwagger()
		if err != nil {
			t.Fatalf("GetSwagger failed: %v", err)
		}
		swagger.Servers = nil

		sesMock := &mockCompSessionService{
			finalizeErrors: map[string]error{
				"ses-failed": fmt.Errorf("bungie timeout"),
			},
		}
		server := Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-internal-key",
		}

		r := gin.New()
		r.ContextWithFallback = true
		r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
			ErrorHandler: logger.GinValidationErrorHandler,
			Options: openapi3filter.Options{
				AuthenticationFunc: validator.Authenticate,
			},
		}))
		h := api.NewStrictHandler(server, []api.StrictMiddlewareFunc{
			logger.GinStrictMiddleware[api.StrictHandlerFunc],
		})
		api.RegisterHandlersWithOptions(r, h, api.GinServerOptions{
			ErrorHandler: logger.GinParamErrorHandler,
		})

		// Missing key -> 401
		reqNoKey := httptest.NewRequest(http.MethodPost, "/internal/sessions/finalize-comp", strings.NewReader(`{"sessionIds":["ses-updated"]}`))
		reqNoKey.Header.Set("Content-Type", "application/json")
		wNoKey := httptest.NewRecorder()
		r.ServeHTTP(wNoKey, reqNoKey)
		if wNoKey.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when X-Internal-API-Key is missing, got %d: %s", wNoKey.Code, wNoKey.Body.String())
		}

		// Wrong key -> 401
		reqBadKey := httptest.NewRequest(http.MethodPost, "/internal/sessions/finalize-comp", strings.NewReader(`{"sessionIds":["ses-updated"]}`))
		reqBadKey.Header.Set("Content-Type", "application/json")
		reqBadKey.Header.Set("X-Internal-API-Key", "wrong-key")
		wBadKey := httptest.NewRecorder()
		r.ServeHTTP(wBadKey, reqBadKey)
		if wBadKey.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when X-Internal-API-Key is invalid, got %d: %s", wBadKey.Code, wBadKey.Body.String())
		}

		// Valid key with clean batch -> 204 No Content
		reqOK := httptest.NewRequest(http.MethodPost, "/internal/sessions/finalize-comp", strings.NewReader(`{"sessionIds":["ses-updated","","ses-skipped"]}`))
		reqOK.Header.Set("Content-Type", "application/json")
		reqOK.Header.Set("X-Internal-API-Key", "secret-internal-key")
		wOK := httptest.NewRecorder()
		r.ServeHTTP(wOK, reqOK)
		if wOK.Code != http.StatusNoContent {
			t.Fatalf("expected 204 No Content, got %d: %s", wOK.Code, wOK.Body.String())
		}
		if len(sesMock.finalizedIDs) != 2 {
			t.Fatalf("expected 2 non-empty session IDs forwarded to FinalizeCompTracking, got %v", sesMock.finalizedIDs)
		}

		// Valid key with failing item -> 500 so server-tick can retry and wide event logs ERROR
		sesMock.finalizedIDs = nil
		reqFail := httptest.NewRequest(http.MethodPost, "/internal/sessions/finalize-comp", strings.NewReader(`{"sessionIds":["ses-updated","ses-failed","ses-skipped"]}`))
		reqFail.Header.Set("Content-Type", "application/json")
		reqFail.Header.Set("X-Internal-API-Key", "secret-internal-key")
		wFail := httptest.NewRecorder()
		r.ServeHTTP(wFail, reqFail)
		if wFail.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 when batch contains failures, got %d: %s", wFail.Code, wFail.Body.String())
		}
		if len(sesMock.finalizedIDs) != 3 {
			t.Fatalf("expected all 3 non-empty session IDs attempted before returning 500, got %v", sesMock.finalizedIDs)
		}
	})
}

func TestSearchEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger failed: %v", err)
	}
	swagger.Servers = nil

	userMock := &mockUserService{
		searchResults: []api.SearchUserResult{
			{
				IsRecorded:          true,
				UserID:              new("user-1"),
				PrimaryMembershipID: "4611686018467484863",
				DisplayName:         "Weegee",
				UniqueName:          "Weegee#1234",
				AlternateNames:      []string{"WeegeeSteam"},
			},
		},
		searchHasMore: true,
	}
	server := Server{UserService: userMock}

	r := gin.New()
	r.ContextWithFallback = true
	r.Use(logger.CanonicalLogger())
	r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		ErrorHandler: logger.GinValidationErrorHandler,
		Options: openapi3filter.Options{
			AuthenticationFunc: validator.Authenticate,
		},
	}))
	h := api.NewStrictHandler(server, []api.StrictMiddlewareFunc{
		logger.GinStrictMiddleware[api.StrictHandlerFunc],
	})
	api.RegisterHandlersWithOptions(r, h, api.GinServerOptions{
		ErrorHandler: logger.GinParamErrorHandler,
	})

	t.Run("public unauthenticated POST /search returns 200 with results and hasMore", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"prefix":"  Weegee  ","page":0}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK without auth headers, got %d: %s", w.Code, w.Body.String())
		}
		if userMock.capturedSearchQuery != "Weegee" || userMock.capturedSearchPage != 0 {
			t.Errorf("expected trimmed query=Weegee page=0, got query=%q page=%d", userMock.capturedSearchQuery, userMock.capturedSearchPage)
		}
		body := w.Body.String()
		if !strings.Contains(body, `"isRecorded":true`) || !strings.Contains(body, `"hasMore":true`) {
			t.Errorf("unexpected response body: %s", body)
		}
	})

	t.Run("whitespace-only prefix returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"prefix":"   ","page":0}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for whitespace prefix, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("negative page returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"prefix":"Weegee","page":-1}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for negative page, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("service error returns 500", func(t *testing.T) {
		userMock.searchErr = fmt.Errorf("firestore unavailable")
		defer func() { userMock.searchErr = nil }()

		req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"prefix":"Weegee","page":0}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 Internal Server Error, got %d: %s", w.Code, w.Body.String())
		}
	})
}

type mockTickSessionService struct {
	session.Service
	capturedOpts session.TickOptions
	calls        int
	result       *api.SessionTickResponse
	err          error
}

func (m *mockTickSessionService) RunTick(_ context.Context, opts session.TickOptions) (*api.SessionTickResponse, error) {
	m.calls++
	m.capturedOpts = opts
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &api.SessionTickResponse{
		DryRun:     opts.DryRun,
		DurationMs: 12,
	}, nil
}

func TestRunSessionTickHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger failed: %v", err)
	}
	swagger.Servers = nil

	newRouter := func(srv Server) *gin.Engine {
		r := gin.New()
		r.ContextWithFallback = true
		r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
			ErrorHandler: logger.GinValidationErrorHandler,
			Options: openapi3filter.Options{
				AuthenticationFunc: validator.Authenticate,
			},
		}))
		h := api.NewStrictHandler(srv, []api.StrictMiddlewareFunc{
			logger.GinStrictMiddleware[api.StrictHandlerFunc],
		})
		api.RegisterHandlersWithOptions(r, h, api.GinServerOptions{
			ErrorHandler: logger.GinParamErrorHandler,
		})
		return r
	}

	t.Run("missing or invalid X-Internal-API-Key returns 401", func(t *testing.T) {
		sesMock := &mockTickSessionService{}
		r := newRouter(Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-tick-key",
		})

		// Missing header
		reqMissing := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{}`))
		reqMissing.Header.Set("Content-Type", "application/json")
		wMissing := httptest.NewRecorder()
		r.ServeHTTP(wMissing, reqMissing)
		if wMissing.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when key missing, got %d: %s", wMissing.Code, wMissing.Body.String())
		}

		// Empty header directly on handler -> 401
		emptyKey := ""
		emptyResp, err := (Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-tick-key",
		}).RunSessionTick(context.Background(), api.RunSessionTickRequestObject{
			Params: api.RunSessionTickParams{XInternalAPIKey: &emptyKey},
		})
		if err != nil {
			t.Fatalf("RunSessionTick with empty key returned unexpected error: %v", err)
		}
		if _, ok := emptyResp.(api.RunSessionTick401JSONResponse); !ok {
			t.Fatalf("expected 401 when key empty, got %T", emptyResp)
		}

		// Invalid key
		reqBad := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{}`))
		reqBad.Header.Set("Content-Type", "application/json")
		reqBad.Header.Set("X-Internal-API-Key", "wrong-key")
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when key invalid, got %d: %s", wBad.Code, wBad.Body.String())
		}

		// Unconfigured Server.InternalAPIKey
		rNoServerKey := newRouter(Server{
			SessionService: sesMock,
			InternalAPIKey: "",
		})
		reqUnconf := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{}`))
		reqUnconf.Header.Set("Content-Type", "application/json")
		reqUnconf.Header.Set("X-Internal-API-Key", "secret-tick-key")
		wUnconf := httptest.NewRecorder()
		rNoServerKey.ServeHTTP(wUnconf, reqUnconf)
		if wUnconf.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when server InternalAPIKey is empty, got %d: %s", wUnconf.Code, wUnconf.Body.String())
		}

		if sesMock.calls != 0 {
			t.Fatalf("expected 0 RunTick calls on unauthorized requests, got %d", sesMock.calls)
		}
	})

	t.Run("default options via empty JSON object and nil Body return 200", func(t *testing.T) {
		sesMock := &mockTickSessionService{
			result: &api.SessionTickResponse{
				DryRun:            false,
				TotalSessions:     4,
				SessionsProcessed: 3,
				SessionsSkipped:   1,
				DurationMs:        42,
			},
		}
		srv := Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-tick-key",
		}
		r := newRouter(srv)

		req := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-API-Key", "secret-tick-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if sesMock.capturedOpts.DryRun || sesMock.capturedOpts.SkipSave {
			t.Fatalf("expected default options (false, false), got %+v", sesMock.capturedOpts)
		}

		var got api.SessionTickResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if got.TotalSessions != 4 || got.SessionsProcessed != 3 || got.SessionsSkipped != 1 || got.DurationMs != 42 {
			t.Fatalf("unexpected SessionTickResponse: %+v", got)
		}

		// Direct call with nil Body
		key := "secret-tick-key"
		directResp, err := srv.RunSessionTick(clog.NewContext(context.Background()), api.RunSessionTickRequestObject{
			Params: api.RunSessionTickParams{XInternalAPIKey: &key},
			Body:   nil,
		})
		if err != nil {
			t.Fatalf("RunSessionTick with nil body failed: %v", err)
		}
		if _, ok := directResp.(api.RunSessionTick200JSONResponse); !ok {
			t.Fatalf("expected RunSessionTick200JSONResponse, got %T", directResp)
		}
		if sesMock.capturedOpts.DryRun || sesMock.capturedOpts.SkipSave {
			t.Fatalf("expected default options for nil body, got %+v", sesMock.capturedOpts)
		}

		// Empty HTTP body rejected with 400 per OpenAPI required requestBody
		reqEmpty := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", nil)
		reqEmpty.Header.Set("Content-Type", "application/json")
		reqEmpty.Header.Set("X-Internal-API-Key", "secret-tick-key")
		wEmpty := httptest.NewRecorder()
		r.ServeHTTP(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when required JSON body is omitted, got %d: %s", wEmpty.Code, wEmpty.Body.String())
		}
	})

	t.Run("explicit dryRun and skipSave options are forwarded and enriched via clog", func(t *testing.T) {
		sesMock := &mockTickSessionService{
			result: &api.SessionTickResponse{
				DryRun:        true,
				TotalSessions: 2,
				DurationMs:    15,
			},
		}
		srv := Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-tick-key",
		}
		r := newRouter(srv)

		req := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{"dryRun":true,"skipSave":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-API-Key", "secret-tick-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if !sesMock.capturedOpts.DryRun || !sesMock.capturedOpts.SkipSave {
			t.Fatalf("expected TickOptions{DryRun: true, SkipSave: true}, got %+v", sesMock.capturedOpts)
		}

		// Verify clog enrichment directly
		ctx := clog.NewContext(context.Background())
		key := "secret-tick-key"
		_, err := srv.RunSessionTick(ctx, api.RunSessionTickRequestObject{
			Params: api.RunSessionTickParams{XInternalAPIKey: &key},
			Body: &api.RunSessionTickJSONRequestBody{
				DryRun:   new(true),
				SkipSave: new(true),
			},
		})
		if err != nil {
			t.Fatalf("RunSessionTick failed: %v", err)
		}
		if !clog.BagFromContext(ctx).HasGroup("request") {
			t.Fatal("expected request group enriched on clog FieldBag")
		}
	})

	t.Run("service error maps to 500 JSON response", func(t *testing.T) {
		sesMock := &mockTickSessionService{
			err: fmt.Errorf("firestore unavailable"),
		}
		r := newRouter(Server{
			SessionService: sesMock,
			InternalAPIKey: "secret-tick-key",
		})

		req := httptest.NewRequest(http.MethodPost, "/internal/sessions/tick", strings.NewReader(`{"dryRun":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-API-Key", "secret-tick-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 Internal Server Error, got %d: %s", w.Code, w.Body.String())
		}
		var errResp api.OneTrickError
		if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to unmarshal OneTrickError: %v", err)
		}
		if !strings.Contains(errResp.Message, "firestore unavailable") {
			t.Fatalf("expected error message to include service error, got %q", errResp.Message)
		}
	})
}

type mockBackfillSnapshotService struct {
	snapshot.Service
	snapshots   []api.CharacterSnapshot
	updatedData map[string]map[string]any
}

func (m *mockBackfillSnapshotService) GetAll(_ context.Context) ([]api.CharacterSnapshot, error) {
	return m.snapshots, nil
}

func (m *mockBackfillSnapshotService) Update(_ context.Context, snapshotID string, updateFn func(data map[string]any) error) error {
	data := make(map[string]any)
	if err := updateFn(data); err != nil {
		return err
	}
	if m.updatedData == nil {
		m.updatedData = make(map[string]map[string]any)
	}
	m.updatedData[snapshotID] = data
	return nil
}

type mockBackfillManifestService struct {
	destiny.ManifestService
	items map[int64]*destiny.ItemDefinition
}

func (m *mockBackfillManifestService) GetItem(_ context.Context, hash int64) (*destiny.ItemDefinition, error) {
	item, ok := m.items[hash]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return item, nil
}

func TestBackfillSnapshotInfoPopulatesEquipableItemSetHash(t *testing.T) {
	setHash := int64(500001)
	snapMock := &mockBackfillSnapshotService{
		snapshots: []api.CharacterSnapshot{
			{
				ID: "snap-1",
				Loadout: api.Loadout{
					"3448274439": {
						ItemHash: 111222,
						ItemProperties: api.ItemProperties{
							BaseInfo: api.BaseItemInfo{ItemHash: 111222},
						},
					},
				},
			},
		},
	}
	manifestMock := &mockBackfillManifestService{
		items: map[int64]*destiny.ItemDefinition{
			111222: {
				Hash:                       111222,
				ItemTypeDisplayName:        "Helmet",
				ItemTypeAndTierDisplayName: "Legendary Helmet",
				Inventory:                  destiny.Inventory{TierType: 5, TierTypeName: "Legendary"},
				EquippingBlock:             destiny.EquippingBlock{EquipableItemSetHash: &setHash},
			},
		},
	}
	srv := Server{
		SnapshotService:   snapMock,
		D2ManifestService: manifestMock,
	}

	resp, err := srv.BackfillSnapshotInfo(context.Background(), api.BackfillSnapshotInfoRequestObject{})
	if err != nil {
		t.Fatalf("BackfillSnapshotInfo failed: %v", err)
	}
	okResp, ok := resp.(api.BackfillSnapshotInfo200JSONResponse)
	if !ok || okResp.Updated != 1 || okResp.Failed != 0 {
		t.Fatalf("expected Updated=1 Failed=0, got %+v", resp)
	}
	updatedLoadout, ok := snapMock.updatedData["snap-1"]["loadout"].(api.Loadout)
	if !ok {
		t.Fatalf("expected updated loadout map, got %T", snapMock.updatedData["snap-1"]["loadout"])
	}
	gotHash := updatedLoadout["3448274439"].ItemProperties.BaseInfo.EquipableItemSetHash
	if gotHash == nil || *gotHash != setHash {
		t.Fatalf("expected backfilled EquipableItemSetHash=%d, got %v", setHash, gotHash)
	}
}
