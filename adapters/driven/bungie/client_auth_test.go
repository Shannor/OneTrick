package bungie

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"sync/atomic"
	"testing"
	"time"
)

type stubRefresher struct {
	token domain.OAuthToken
	err   error
	calls atomic.Int32
}

func (s *stubRefresher) RefreshAccessToken(_ context.Context, _ string) (domain.OAuthToken, error) {
	s.calls.Add(1)
	return s.token, s.err
}

func TestNewClient_HeadersMidRequestOAuthRefreshAndThrottleRetry(t *testing.T) {
	t.Run("injects Bungie headers and refreshes bearer token once on 401", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-API-KEY"); got != "test-api-key" {
				t.Errorf("expected X-API-KEY=test-api-key, got %q", got)
			}
			if got := r.Header.Get("User-Agent"); got != "oneTrick-backend" {
				t.Errorf("expected User-Agent=oneTrick-backend, got %q", got)
			}
			n := attempts.Add(1)
			auth := r.Header.Get("Authorization")
			if n == 1 {
				if auth != "Bearer expired-token" {
					t.Errorf("attempt 1: expected Bearer expired-token, got %q", auth)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"ErrorCode":99,"Message":"Unauthorized"}`)
				return
			}
			if auth != "Bearer fresh-token" {
				t.Errorf("attempt 2: expected Bearer fresh-token, got %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ErrorCode":1,"Response":{"profileTransitoryData":{"data":{"partyMembers":[{"membershipId":"42"}]}}}}`)
		}))
		defer srv.Close()

		refresher := &stubRefresher{
			token: domain.OAuthToken{
				AccessToken:  "fresh-token",
				RefreshToken: "fresh-refresh",
				ExpiresIn:    3600,
			},
		}
		cli, err := NewClient(
			"test-api-key",
			srv.Client(),
			WithBaseURL(srv.URL),
			WithOAuthRefresher(refresher),
			WithThrottleRetry(1, 5*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}

		var callbackTok domain.OAuthToken
		ctx := WithAuthCredentials(context.Background(), AuthCredentials{
			AccessToken:  "expired-token",
			RefreshToken: "valid-refresh",
			OnRefresh: func(tok domain.OAuthToken) {
				callbackTok = tok
			},
		})

		partyGW := NewPartyGateway(cli)
		ids, err := partyGW.GetPartyMembershipIDs(ctx, 1001, 3)
		if err != nil {
			t.Fatalf("expected 401 refresh replay to succeed, got %v", err)
		}
		if len(ids) != 1 || ids[0] != "42" {
			t.Fatalf("unexpected party IDs: %+v", ids)
		}
		if refresher.calls.Load() != 1 {
			t.Fatalf("expected 1 refresh call, got %d", refresher.calls.Load())
		}
		if callbackTok.AccessToken != "fresh-token" {
			t.Fatalf("expected OnRefresh callback with fresh-token, got %+v", callbackTok)
		}
	})

	t.Run("retries transient throttle responses and maps exhausted throttle to ErrBungieThrottled", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"ErrorCode":36,"ThrottleSeconds":1,"ErrorStatus":"ThrottleLimitExceededMinutes"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"ErrorCode":1,"Response":{"profileTransitoryData":{"data":{"partyMembers":[{"membershipId":"99"}]}}}}`)
		}))
		defer srv.Close()

		cli, err := NewClient(
			"test-api-key",
			srv.Client(),
			WithBaseURL(srv.URL),
			WithThrottleRetry(2, 5*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}

		partyGW := NewPartyGateway(cli)
		ids, err := partyGW.GetPartyMembershipIDs(context.Background(), 1001, 3)
		if err != nil {
			t.Fatalf("expected retry after transient 429 to succeed, got %v", err)
		}
		if len(ids) != 1 || ids[0] != "99" || calls.Load() != 2 {
			t.Fatalf("unexpected ids=%+v calls=%d", ids, calls.Load())
		}

		alwaysThrottled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ErrorCode":36,"ThrottleSeconds":1,"ErrorStatus":"ThrottleLimitExceededMinutes"}`)
		}))
		defer alwaysThrottled.Close()

		cliExhausted, err := NewClient(
			"test-api-key",
			alwaysThrottled.Client(),
			WithBaseURL(alwaysThrottled.URL),
			WithThrottleRetry(1, 5*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		_, err = NewPartyGateway(cliExhausted).GetPartyMembershipIDs(context.Background(), 1001, 3)
		if !errors.Is(err, domain.ErrBungieThrottled) {
			t.Fatalf("expected domain.ErrBungieThrottled after retry exhaustion, got %v", err)
		}
	})
}

type fakeAuthBungieClient struct {
	bungie.ClientWithResponsesInterface
	resp       *bungie.UserGetMembershipDataForCurrentUserResponse
	err        error
	authHeader string
}

func (f *fakeAuthBungieClient) UserGetMembershipDataForCurrentUserWithResponse(
	ctx context.Context,
	reqEditors ...bungie.RequestEditorFn,
) (*bungie.UserGetMembershipDataForCurrentUserResponse, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test", nil)
	for _, ed := range reqEditors {
		_ = ed(ctx, req)
	}
	f.authHeader = req.Header.Get("Authorization")
	return f.resp, f.err
}

func TestOAuthGateway(t *testing.T) {
	t.Run("ExchangeCode and RefreshAccessToken use stdlib form encoding and Basic auth", func(t *testing.T) {
		expectedBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:csecret"))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != expectedBasic {
				t.Errorf("expected Basic auth %q, got %q", expectedBasic, r.Header.Get("Authorization"))
			}
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Errorf("unexpected Content-Type %q", r.Header.Get("Content-Type"))
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if r.Form.Get("code") != "auth-code-1234" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"bad code"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":       "acc-1",
					"token_type":         "Bearer",
					"expires_in":         3600,
					"refresh_token":      "ref-1",
					"refresh_expires_in": 7776000,
					"membership_id":      "999888",
				})
			case "refresh_token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":       "acc-2",
					"token_type":         "Bearer",
					"expires_in":         3600,
					"refresh_token":      "ref-2",
					"refresh_expires_in": 7776000,
					"membership_id":      "999888",
				})
			}
		}))
		defer srv.Close()

		gw := NewOAuthGateway(srv.Client(), nil, "cid", "csecret", WithTokenEndpoint(srv.URL))

		tok, err := gw.ExchangeCode(context.Background(), "auth-code-1234")
		if err != nil {
			t.Fatalf("ExchangeCode failed: %v", err)
		}
		if tok.AccessToken != "acc-1" || tok.RefreshToken != "ref-1" || tok.MembershipID != "999888" {
			t.Fatalf("unexpected exchanged token: %+v", tok)
		}

		if _, err := gw.ExchangeCode(context.Background(), "wrong"); err == nil {
			t.Fatal("expected error on 400 oauth response")
		}

		refreshed, err := gw.RefreshAccessToken(context.Background(), "ref-1")
		if err != nil {
			t.Fatalf("RefreshAccessToken failed: %v", err)
		}
		if refreshed.AccessToken != "acc-2" {
			t.Fatalf("unexpected refreshed token: %+v", refreshed)
		}
	})

	t.Run("GetCurrentAccount and HasAccess translate Bungie membership data cleanly", func(t *testing.T) {
		fake := &fakeAuthBungieClient{
			resp: &bungie.UserGetMembershipDataForCurrentUserResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserUserMembershipData{
					ErrorCode: new(int32(1)),
					MembershipData: &bungie.MembershipData{
						PrimaryMembershipId: new("4611686018467484863"),
						BungieNetUser: &bungie.UserGeneralUser{
							MembershipId: new("bnet-1"),
							DisplayName:  new("Guardian"),
							UniqueName:   new("Guardian#1234"),
						},
						DestinyMemberships: &[]bungie.GroupsV2GroupUserInfoCard{
							{
								MembershipId:   new("4611686018467484863"),
								MembershipType: new(int32(3)),
								DisplayName:    new("GuardianSteam"),
							},
						},
					},
				},
			},
		}
		gw := NewOAuthGateway(nil, fake, "cid", "csecret")

		acct, err := gw.GetCurrentAccount(context.Background(), "tok-abc")
		if err != nil {
			t.Fatalf("GetCurrentAccount failed: %v", err)
		}
		if fake.authHeader != "Bearer tok-abc" {
			t.Fatalf("expected Bearer tok-abc header, got %q", fake.authHeader)
		}
		if acct.BungieNetMembershipID != "bnet-1" || acct.PrimaryMembershipID != "4611686018467484863" || len(acct.DestinyMemberships) != 1 {
			t.Fatalf("unexpected BungieAccount: %+v", acct)
		}

		ok, err := gw.HasAccess(context.Background(), "bnet-1", "tok-abc")
		if err != nil || !ok {
			t.Fatalf("expected HasAccess=true, got ok=%v err=%v", ok, err)
		}
		ok, err = gw.HasAccess(context.Background(), "other-bnet", "tok-abc")
		if err != nil || ok {
			t.Fatalf("expected HasAccess=false, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("MaskOAuthCode masks short and long codes", func(t *testing.T) {
		if got := MaskOAuthCode(""); got != "<empty>" {
			t.Fatalf("expected <empty>, got %q", got)
		}
		if got := MaskOAuthCode("12345678"); got != "***" {
			t.Fatalf("expected ***, got %q", got)
		}
		if got := MaskOAuthCode("abcd12345678wxyz"); got != "abcd...wxyz" {
			t.Fatalf("expected abcd...wxyz, got %q", got)
		}
	})
}
