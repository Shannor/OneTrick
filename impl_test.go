package main

import (
	"context"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"testing"
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
	existingUser *user.User
	createdUser  *user.User
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
}

func (m *mockStatsService) GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error) {
	return []api.FeaturedLoadout{
		{
			FeaturedReason: ptr.Of("Top Hunter PvP Loadout of the Day"),
			UsageCount:     ptr.Of(5),
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
			Count: ptr.Of(5),
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
