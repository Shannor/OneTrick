package destiny

import (
	"context"
	"errors"
	"net/http"
	"oneTrick/clients/bungie"
	"reflect"
	"testing"
)

type fakeSearchBungieClient struct {
	bungie.ClientWithResponsesInterface

	globalResp         *bungie.UserSearchByGlobalNamePostResponse
	globalErr          error
	capturedPage       int32
	capturedPrefixBody bungie.UserSearchByGlobalNamePostJSONRequestBody
	globalCalls        int

	exactResp              *bungie.Destiny2SearchDestinyPlayerByBungieNameResponse
	exactErr               error
	capturedMembershipType int32
	capturedExactBody      bungie.Destiny2SearchDestinyPlayerByBungieNameJSONRequestBody
	exactCalls             int
}

func (f *fakeSearchBungieClient) UserSearchByGlobalNamePostWithResponse(
	_ context.Context,
	page int32,
	body bungie.UserSearchByGlobalNamePostJSONRequestBody,
	_ ...bungie.RequestEditorFn,
) (*bungie.UserSearchByGlobalNamePostResponse, error) {
	f.globalCalls++
	f.capturedPage = page
	f.capturedPrefixBody = body
	return f.globalResp, f.globalErr
}

func (f *fakeSearchBungieClient) Destiny2SearchDestinyPlayerByBungieNameWithResponse(
	_ context.Context,
	membershipType int32,
	body bungie.Destiny2SearchDestinyPlayerByBungieNameJSONRequestBody,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2SearchDestinyPlayerByBungieNameResponse, error) {
	f.exactCalls++
	f.capturedMembershipType = membershipType
	f.capturedExactBody = body
	return f.exactResp, f.exactErr
}

func TestSearchPrefixMappingAndFiltering(t *testing.T) {
	client := &fakeSearchBungieClient{
		globalResp: &bungie.UserSearchByGlobalNamePostResponse{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK},
			JSON200: &bungie.UserSearchResponse{
				SearchResponse: &bungie.SearchResponse{
					HasMore: new(true),
					SearchResults: &[]bungie.UserSearchDetail{
						{
							BungieNetMembershipId:       new("999001"),
							BungieGlobalDisplayName:     new("Weegee"),
							BungieGlobalDisplayNameCode: new(int16(42)),
							DestinyMemberships: &[]bungie.UserUserInfoCard{
								{
									MembershipId:              new("4611686018467484863"),
									MembershipType:            new(int32(3)),
									DisplayName:               new("WeegeeSteam"),
									IconPath:                  new("/img/theme/bungienet/icons/steamLogo.png"),
									CrossSaveOverride:         new(int32(0)),
									ApplicableMembershipTypes: &[]int32{3},
								},
								{
									MembershipId:              new("4611686018400000001"),
									MembershipType:            new(int32(1)),
									DisplayName:               new("WeegeeXbox"),
									CrossSaveOverride:         new(int32(3)),
									ApplicableMembershipTypes: &[]int32{},
								},
								{
									MembershipId:              new("4611686018400000002"),
									MembershipType:            new(int32(2)),
									DisplayName:               new("WeegeeSteam"),
									CrossSaveOverride:         new(int32(3)),
									ApplicableMembershipTypes: &[]int32{},
								},
							},
						},
						{
							// Bungie.net account with zero Destiny memberships should be filtered out.
							BungieNetMembershipId:       new("999002"),
							BungieGlobalDisplayName:     new("WeegeeNoD2"),
							BungieGlobalDisplayNameCode: new(int16(1234)),
							DestinyMemberships:          &[]bungie.UserUserInfoCard{},
						},
						{
							// Nil DestinyMemberships should also be filtered out.
							BungieNetMembershipId:       new("999003"),
							BungieGlobalDisplayName:     new("WeegeeNilD2"),
							BungieGlobalDisplayNameCode: new(int16(5678)),
							DestinyMemberships:          nil,
						},
					},
				},
			},
		},
	}

	svc := &service{Client: client}
	results, hasMore, err := svc.Search(context.Background(), "  Weegee  ", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasMore {
		t.Fatalf("expected hasMore=true, got false")
	}
	if client.globalCalls != 1 || client.capturedPage != 1 {
		t.Fatalf("expected 1 global search call on page 1, got calls=%d page=%d", client.globalCalls, client.capturedPage)
	}
	if client.capturedPrefixBody.DisplayNamePrefix == nil || *client.capturedPrefixBody.DisplayNamePrefix != "Weegee" {
		t.Fatalf("expected DisplayNamePrefix=Weegee, got %v", client.capturedPrefixBody.DisplayNamePrefix)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after filtering zero-membership accounts, got %d", len(results))
	}

	got := results[0]
	if got.IsRecorded {
		t.Errorf("expected IsRecorded=false, got true")
	}
	if got.DisplayName != "Weegee" {
		t.Errorf("expected DisplayName=Weegee, got %q", got.DisplayName)
	}
	if got.UniqueName != "Weegee#0042" {
		t.Errorf("expected UniqueName=Weegee#0042, got %q", got.UniqueName)
	}
	if got.PrimaryMembershipID != "4611686018467484863" {
		t.Errorf("expected PrimaryMembershipID=4611686018467484863, got %q", got.PrimaryMembershipID)
	}
	if got.PrimaryMembershipType == nil || *got.PrimaryMembershipType != 3 {
		t.Errorf("expected PrimaryMembershipType=3, got %v", got.PrimaryMembershipType)
	}
	if got.BungieNetMembershipID == nil || *got.BungieNetMembershipID != "999001" {
		t.Errorf("expected BungieNetMembershipID=999001, got %v", got.BungieNetMembershipID)
	}
	if got.IconPath == nil || *got.IconPath != "/img/theme/bungienet/icons/steamLogo.png" {
		t.Errorf("expected IconPath=/img/theme/bungienet/icons/steamLogo.png, got %v", got.IconPath)
	}
	wantAlt := []string{"WeegeeSteam", "WeegeeXbox"}
	if !reflect.DeepEqual(got.AlternateNames, wantAlt) {
		t.Errorf("expected AlternateNames=%v, got %v", wantAlt, got.AlternateNames)
	}
}

func TestSearchCrossSavePrimaryMembershipResolution(t *testing.T) {
	t.Run("prefers cross-save override matching membership type when all cards have CrossSaveOverride set", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					SearchResponse: &bungie.SearchResponse{
						HasMore: new(false),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName:     new("CrossSaver"),
								BungieGlobalDisplayNameCode: new(int16(1234)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{
										MembershipId:              new("xbox-overridden"),
										MembershipType:            new(int32(1)),
										CrossSaveOverride:         new(int32(3)),
										ApplicableMembershipTypes: &[]int32{},
										DisplayName:               new("XboxName"),
									},
									{
										MembershipId:              new("steam-primary"),
										MembershipType:            new(int32(3)),
										CrossSaveOverride:         new(int32(3)),
										ApplicableMembershipTypes: &[]int32{1, 3},
										DisplayName:               new("SteamName"),
									},
								},
							},
						},
					},
				},
			},
		}

		svc := &service{Client: client}
		results, _, err := svc.Search(context.Background(), "CrossSaver", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].PrimaryMembershipID != "steam-primary" {
			t.Errorf("expected steam-primary, got %q", results[0].PrimaryMembershipID)
		}
		if results[0].PrimaryMembershipType == nil || *results[0].PrimaryMembershipType != 3 {
			t.Errorf("expected PrimaryMembershipType=3, got %v", results[0].PrimaryMembershipType)
		}
	})

	t.Run("falls back to first membership when cross-save and applicable types are absent", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					SearchResponse: &bungie.SearchResponse{
						HasMore: new(false),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName: new("FallbackUser"),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{
										MembershipId:   new("first-psn"),
										MembershipType: new(int32(2)),
										DisplayName:    new("FirstPSN"),
									},
									{
										MembershipId:   new("second-steam"),
										MembershipType: new(int32(3)),
										DisplayName:    new("SecondSteam"),
									},
								},
							},
						},
					},
				},
			},
		}

		svc := &service{Client: client}
		results, _, err := svc.Search(context.Background(), "FallbackUser", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].PrimaryMembershipID != "first-psn" {
			t.Errorf("expected first-psn, got %q", results[0].PrimaryMembershipID)
		}
		if results[0].UniqueName != "FallbackUser" {
			t.Errorf("expected fallback UniqueName=FallbackUser, got %q", results[0].UniqueName)
		}
	})
}

func TestSearchDiscriminatorParsingAndFiltering(t *testing.T) {
	t.Run("partial discriminator Name#12 filters zero-padded 4-digit codes and requires exact display name match", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					SearchResponse: &bungie.SearchResponse{
						HasMore: new(true),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName:     new("Guardian"),
								BungieGlobalDisplayNameCode: new(int16(1234)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: new("m1"), MembershipType: new(int32(3)), DisplayName: new("G1")},
								},
							},
							{
								BungieGlobalDisplayName:     new("guardian"),
								BungieGlobalDisplayNameCode: new(int16(1299)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: new("m2"), MembershipType: new(int32(1)), DisplayName: new("G2")},
								},
							},
							{
								// Prefix matches "Guardian" and code matches "12", but displayName != "Guardian".
								BungieGlobalDisplayName:     new("GuardianAngel"),
								BungieGlobalDisplayNameCode: new(int16(1255)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: new("m-angel"), MembershipType: new(int32(3)), DisplayName: new("Angel")},
								},
							},
							{
								BungieGlobalDisplayName:     new("Guardian"),
								BungieGlobalDisplayNameCode: new(int16(9876)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: new("m3"), MembershipType: new(int32(2)), DisplayName: new("G3")},
								},
							},
						},
					},
				},
			},
		}

		svc := &service{Client: client}
		results, hasMore, err := svc.Search(context.Background(), "Guardian#12", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasMore {
			t.Errorf("expected hasMore=true")
		}
		if client.exactCalls != 0 || client.globalCalls != 1 {
			t.Fatalf("expected global search only for partial discriminator, got exact=%d global=%d", client.exactCalls, client.globalCalls)
		}
		if client.capturedPrefixBody.DisplayNamePrefix == nil || *client.capturedPrefixBody.DisplayNamePrefix != "Guardian" {
			t.Fatalf("expected prefix Guardian without #12, got %v", client.capturedPrefixBody.DisplayNamePrefix)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 matching results for #12, got %d", len(results))
		}
		if results[0].UniqueName != "Guardian#1234" || results[1].UniqueName != "guardian#1299" {
			t.Errorf("unexpected filtered results: %+v", results)
		}
	})

	t.Run("exact Name#0042 on page 0 calls exact Bungie name endpoint", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.IEnumerableOfUserInfoCard{
					ErrorCode: new(int32(1)),
					Response: &[]bungie.UserUserInfoCard{
						{
							MembershipId:                new("4611686018467484863"),
							MembershipType:              new(int32(3)),
							DisplayName:                 new("ExactSteam"),
							BungieGlobalDisplayName:     new("Guardian"),
							BungieGlobalDisplayNameCode: new(int16(42)),
							IconPath:                    new("/icons/steam.png"),
							CrossSaveOverride:           new(int32(3)),
							ApplicableMembershipTypes:   &[]int32{1, 3},
						},
					},
				},
			},
		}

		svc := &service{Client: client}
		results, hasMore, err := svc.Search(context.Background(), "Guardian#0042", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasMore {
			t.Errorf("expected hasMore=false for exact Bungie name lookup")
		}
		if client.exactCalls != 1 || client.globalCalls != 0 {
			t.Fatalf("expected 1 exact search call and 0 global calls, got exact=%d global=%d", client.exactCalls, client.globalCalls)
		}
		if client.capturedMembershipType != -1 {
			t.Errorf("expected membershipType=-1 (All), got %d", client.capturedMembershipType)
		}
		if client.capturedExactBody.DisplayName == nil || *client.capturedExactBody.DisplayName != "Guardian" {
			t.Errorf("expected DisplayName=Guardian, got %v", client.capturedExactBody.DisplayName)
		}
		if client.capturedExactBody.DisplayNameCode == nil || *client.capturedExactBody.DisplayNameCode != 42 {
			t.Errorf("expected DisplayNameCode=42, got %v", client.capturedExactBody.DisplayNameCode)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 exact result, got %d", len(results))
		}
		if results[0].UniqueName != "Guardian#0042" || results[0].PrimaryMembershipID != "4611686018467484863" {
			t.Errorf("unexpected exact result: %+v", results[0])
		}
	})

	t.Run("exact Name#1234 with page > 0 returns empty without calling Bungie", func(t *testing.T) {
		client := &fakeSearchBungieClient{}
		svc := &service{Client: client}
		results, hasMore, err := svc.Search(context.Background(), "Guardian#1234", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasMore || len(results) != 0 {
			t.Fatalf("expected empty results and hasMore=false, got len=%d hasMore=%v", len(results), hasMore)
		}
		if client.exactCalls != 0 || client.globalCalls != 0 {
			t.Fatalf("expected 0 Bungie calls on page > 0 for exact Bungie name, got exact=%d global=%d", client.exactCalls, client.globalCalls)
		}
	})

	t.Run("empty namePart returns empty without calling Bungie", func(t *testing.T) {
		client := &fakeSearchBungieClient{}
		svc := &service{Client: client}
		results, hasMore, err := svc.Search(context.Background(), "   #1234", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasMore || len(results) != 0 {
			t.Fatalf("expected empty results and hasMore=false, got len=%d hasMore=%v", len(results), hasMore)
		}
		if client.globalCalls != 0 || client.exactCalls != 0 {
			t.Fatalf("expected 0 Bungie calls for empty namePart")
		}
	})
}

func TestSearchHandles503AndBungie200ErrorEnvelopes(t *testing.T) {
	t.Run("prefix search 503", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian", 0)
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown, got %v", err)
		}
	})

	t.Run("exact search 503", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian#1234", 0)
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown, got %v", err)
		}
	})

	t.Run("exact search HTTP 200 SystemDisabled (ErrorCode=5) returns ErrDestinyServerDown", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.IEnumerableOfUserInfoCard{
					ErrorCode:   new(int32(5)),
					ErrorStatus: new("SystemDisabled"),
					Message:     new("This system is temporarily disabled for maintenance."),
					Response:    nil,
				},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian#1234", 0)
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown for ErrorCode=5, got %v", err)
		}
	})

	t.Run("exact search HTTP 200 nil Response returns error", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.IEnumerableOfUserInfoCard{
					ErrorCode: new(int32(1)),
					Response:  nil,
				},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian#1234", 0)
		if err == nil {
			t.Fatal("expected error when exact search JSON200.Response is nil")
		}
	})

	t.Run("prefix search HTTP 200 throttle ErrorCode=36 returns error", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					ErrorCode:   new(int32(36)),
					ErrorStatus: new("ThrottleLimitExceededMinutes"),
					Message:     new("Throttle limit exceeded"),
				},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian", 0)
		if err == nil {
			t.Fatal("expected error when prefix search returns non-1 ErrorCode")
		}
	})

	t.Run("prefix search HTTP 200 SystemDisabled (ErrorCode=5) returns ErrDestinyServerDown", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					ErrorCode:   new(int32(5)),
					ErrorStatus: new("SystemDisabled"),
					Message:     new("Maintenance"),
				},
			},
		}
		svc := &service{Client: client}
		_, _, err := svc.Search(context.Background(), "Guardian", 0)
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown for prefix ErrorCode=5, got %v", err)
		}
	})
}
