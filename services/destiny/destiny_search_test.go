package destiny

import (
	"context"
	"errors"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
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
					HasMore: ptr.Of(true),
					SearchResults: &[]bungie.UserSearchDetail{
						{
							BungieNetMembershipId:       ptr.Of("999001"),
							BungieGlobalDisplayName:     ptr.Of("Weegee"),
							BungieGlobalDisplayNameCode: ptr.Of(int16(42)),
							DestinyMemberships: &[]bungie.UserUserInfoCard{
								{
									MembershipId:              ptr.Of("4611686018467484863"),
									MembershipType:            ptr.Of(int32(3)),
									DisplayName:               ptr.Of("WeegeeSteam"),
									IconPath:                  ptr.Of("/img/theme/bungienet/icons/steamLogo.png"),
									CrossSaveOverride:         ptr.Of(int32(0)),
									ApplicableMembershipTypes: &[]int32{3},
								},
								{
									MembershipId:              ptr.Of("4611686018400000001"),
									MembershipType:            ptr.Of(int32(1)),
									DisplayName:               ptr.Of("WeegeeXbox"),
									CrossSaveOverride:         ptr.Of(int32(3)),
									ApplicableMembershipTypes: &[]int32{},
								},
								{
									MembershipId:              ptr.Of("4611686018400000002"),
									MembershipType:            ptr.Of(int32(2)),
									DisplayName:               ptr.Of("WeegeeSteam"),
									CrossSaveOverride:         ptr.Of(int32(3)),
									ApplicableMembershipTypes: &[]int32{},
								},
							},
						},
						{
							// Bungie.net account with zero Destiny memberships should be filtered out.
							BungieNetMembershipId:       ptr.Of("999002"),
							BungieGlobalDisplayName:     ptr.Of("WeegeeNoD2"),
							BungieGlobalDisplayNameCode: ptr.Of(int16(1234)),
							DestinyMemberships:          &[]bungie.UserUserInfoCard{},
						},
						{
							// Nil DestinyMemberships should also be filtered out.
							BungieNetMembershipId:       ptr.Of("999003"),
							BungieGlobalDisplayName:     ptr.Of("WeegeeNilD2"),
							BungieGlobalDisplayNameCode: ptr.Of(int16(5678)),
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
						HasMore: ptr.Of(false),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName:     ptr.Of("CrossSaver"),
								BungieGlobalDisplayNameCode: ptr.Of(int16(1234)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{
										MembershipId:              ptr.Of("xbox-overridden"),
										MembershipType:            ptr.Of(int32(1)),
										CrossSaveOverride:         ptr.Of(int32(3)),
										ApplicableMembershipTypes: &[]int32{},
										DisplayName:               ptr.Of("XboxName"),
									},
									{
										MembershipId:              ptr.Of("steam-primary"),
										MembershipType:            ptr.Of(int32(3)),
										CrossSaveOverride:         ptr.Of(int32(3)),
										ApplicableMembershipTypes: &[]int32{1, 3},
										DisplayName:               ptr.Of("SteamName"),
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
						HasMore: ptr.Of(false),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName: ptr.Of("FallbackUser"),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{
										MembershipId:   ptr.Of("first-psn"),
										MembershipType: ptr.Of(int32(2)),
										DisplayName:    ptr.Of("FirstPSN"),
									},
									{
										MembershipId:   ptr.Of("second-steam"),
										MembershipType: ptr.Of(int32(3)),
										DisplayName:    ptr.Of("SecondSteam"),
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
	t.Run("partial discriminator Name#12 filters zero-padded 4-digit codes", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					SearchResponse: &bungie.SearchResponse{
						HasMore: ptr.Of(true),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieGlobalDisplayName:     ptr.Of("Guardian"),
								BungieGlobalDisplayNameCode: ptr.Of(int16(1234)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: ptr.Of("m1"), MembershipType: ptr.Of(int32(3)), DisplayName: ptr.Of("G1")},
								},
							},
							{
								BungieGlobalDisplayName:     ptr.Of("Guardian"),
								BungieGlobalDisplayNameCode: ptr.Of(int16(1299)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: ptr.Of("m2"), MembershipType: ptr.Of(int32(1)), DisplayName: ptr.Of("G2")},
								},
							},
							{
								BungieGlobalDisplayName:     ptr.Of("Guardian"),
								BungieGlobalDisplayNameCode: ptr.Of(int16(9876)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{MembershipId: ptr.Of("m3"), MembershipType: ptr.Of(int32(2)), DisplayName: ptr.Of("G3")},
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
		if results[0].UniqueName != "Guardian#1234" || results[1].UniqueName != "Guardian#1299" {
			t.Errorf("unexpected filtered results: %+v", results)
		}
	})

	t.Run("exact Name#0042 on page 0 calls exact Bungie name endpoint", func(t *testing.T) {
		client := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.IEnumerableOfUserInfoCard{
					Response: &[]bungie.UserUserInfoCard{
						{
							MembershipId:                ptr.Of("4611686018467484863"),
							MembershipType:              ptr.Of(int32(3)),
							DisplayName:                 ptr.Of("ExactSteam"),
							BungieGlobalDisplayName:     ptr.Of("Guardian"),
							BungieGlobalDisplayNameCode: ptr.Of(int16(42)),
							IconPath:                    ptr.Of("/icons/steam.png"),
							CrossSaveOverride:           ptr.Of(int32(3)),
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

func TestSearchHandles503AsErrDestinyServerDown(t *testing.T) {
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
}
