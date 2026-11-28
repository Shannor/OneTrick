package bungie

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/ptr"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

const defaultBungieOAuthTokenURL = "https://www.bungie.net/Platform/App/OAuth/Token"

type OAuthOption func(*OAuthGateway)

func WithTokenEndpoint(tokenURL string) OAuthOption {
	return func(g *OAuthGateway) {
		if trimmed := strings.TrimSpace(tokenURL); trimmed != "" {
			g.tokenURL = trimmed
		}
	}
}

type TokenRefresher struct {
	httpClient   *http.Client
	clientID     string
	clientSecret string
	tokenURL     string
}

func NewTokenRefresher(
	httpClient *http.Client,
	clientID string,
	clientSecret string,
	opts ...OAuthOption,
) *TokenRefresher {
	gw := NewOAuthGateway(httpClient, nil, clientID, clientSecret, opts...)
	return &TokenRefresher{
		httpClient:   gw.httpClient,
		clientID:     gw.clientID,
		clientSecret: gw.clientSecret,
		tokenURL:     gw.tokenURL,
	}
}

func (r *TokenRefresher) RefreshAccessToken(ctx context.Context, refreshToken string) (domain.OAuthToken, error) {
	gw := OAuthGateway{
		httpClient:   r.httpClient,
		clientID:     r.clientID,
		clientSecret: r.clientSecret,
		tokenURL:     r.tokenURL,
	}
	return gw.RefreshAccessToken(ctx, refreshToken)
}

type OAuthGateway struct {
	httpClient   *http.Client
	bungieClient bungie.ClientWithResponsesInterface
	clientID     string
	clientSecret string
	tokenURL     string
}

func NewOAuthGateway(
	httpClient *http.Client,
	bungieClient bungie.ClientWithResponsesInterface,
	clientID string,
	clientSecret string,
	opts ...OAuthOption,
) *OAuthGateway {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	gw := &OAuthGateway{
		httpClient:   httpClient,
		bungieClient: bungieClient,
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     defaultBungieOAuthTokenURL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(gw)
		}
	}
	return gw
}

func MaskOAuthCode(code string) string {
	if len(code) == 0 {
		return "<empty>"
	}
	if len(code) <= 8 {
		return "***"
	}
	return code[:4] + "..." + code[len(code)-4:]
}

type oauthTokenWire struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	MembershipID     string `json:"membership_id"`
}

type oauthErrorWire struct {
	ErrorType    string `json:"error"`
	ErrorMessage string `json:"error_description"`
}

func (g *OAuthGateway) ExchangeCode(ctx context.Context, code string) (domain.OAuthToken, error) {
	clog.WithGroupFields(ctx, "bungieOAuth.exchangeCode", clog.Fields{
		"codeLen":     len(code),
		"codeSnippet": MaskOAuthCode(code),
	})

	values := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
	}
	tok, err := g.postTokenForm(ctx, "bungieOAuth.exchangeCode", "getting access token", values)
	if err != nil {
		return domain.OAuthToken{}, err
	}
	clog.WithGroupFields(ctx, "bungieOAuth.exchangeCode", clog.Fields{
		"membershipId": tok.MembershipID,
		"expiresIn":    tok.ExpiresIn,
	})
	return tok, nil
}

func (g *OAuthGateway) RefreshAccessToken(ctx context.Context, refreshToken string) (domain.OAuthToken, error) {
	clog.WithGroupField(ctx, "bungieOAuth.refreshAccessToken", "refreshTokenLen", len(refreshToken))

	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	tok, err := g.postTokenForm(ctx, "bungieOAuth.refreshAccessToken", "refreshing access token", values)
	if err != nil {
		return domain.OAuthToken{}, err
	}
	clog.WithGroupFields(ctx, "bungieOAuth.refreshAccessToken", clog.Fields{
		"membershipId": tok.MembershipID,
		"expiresIn":    tok.ExpiresIn,
	})
	return tok, nil
}

func (g *OAuthGateway) postTokenForm(
	ctx context.Context,
	logGroup string,
	actionDesc string,
	values url.Values,
) (domain.OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return domain.OAuthToken{}, err
	}
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(g.clientID + ":" + g.clientSecret))
	req.Header.Set("Authorization", "Basic "+encodedCreds)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return domain.OAuthToken{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.OAuthToken{}, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var errWire oauthErrorWire
		_ = json.Unmarshal(raw, &errWire)
		clog.WithGroupFields(ctx, logGroup, clog.Fields{
			"statusCode":       resp.StatusCode,
			"errorType":        errWire.ErrorType,
			"errorDescription": errWire.ErrorMessage,
		})
		if resp.StatusCode == http.StatusServiceUnavailable {
			return domain.OAuthToken{}, domain.ErrDestinyUnavailable
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return domain.OAuthToken{}, domain.ErrBungieThrottled
		}
		return domain.OAuthToken{}, fmt.Errorf("error %s: %s: %s", actionDesc, errWire.ErrorType, errWire.ErrorMessage)
	}

	var wire oauthTokenWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return domain.OAuthToken{}, fmt.Errorf("failed to decode oauth token response: %w", err)
	}
	return domain.OAuthToken{
		AccessToken:      wire.AccessToken,
		TokenType:        wire.TokenType,
		ExpiresIn:        wire.ExpiresIn,
		RefreshToken:     wire.RefreshToken,
		RefreshExpiresIn: wire.RefreshExpiresIn,
		MembershipID:     wire.MembershipID,
	}, nil
}

func (g *OAuthGateway) GetCurrentAccount(ctx context.Context, accessToken string) (domain.BungieAccount, error) {
	resp, err := g.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return nil
	})
	if err != nil {
		return domain.BungieAccount{}, err
	}
	if resp == nil {
		return domain.BungieAccount{}, fmt.Errorf("no membership data")
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieOAuth.getCurrentAccount", resp.StatusCode(), resp.Body, env); err != nil {
		return domain.BungieAccount{}, err
	}
	if resp.JSON200 == nil || resp.JSON200.MembershipData == nil {
		clog.WithGroupField(ctx, "bungieOAuth.getCurrentAccount", "statusCode", resp.StatusCode())
		return domain.BungieAccount{}, fmt.Errorf("no membership data")
	}

	data := resp.JSON200.MembershipData
	if data.BungieNetUser == nil && data.DestinyMemberships == nil {
		return domain.BungieAccount{}, fmt.Errorf("failed to fetch user data")
	}

	account := domain.BungieAccount{
		PrimaryMembershipID: ptr.Deref(data.PrimaryMembershipId),
	}
	if data.BungieNetUser != nil {
		account.BungieNetMembershipID = ptr.Deref(data.BungieNetUser.MembershipId)
		account.DisplayName = ptr.Deref(data.BungieNetUser.DisplayName)
		account.UniqueName = ptr.Deref(data.BungieNetUser.UniqueName)
	}
	if data.DestinyMemberships != nil {
		memberships := make([]domain.Membership, 0, len(*data.DestinyMemberships))
		for _, mem := range *data.DestinyMemberships {
			if mem.MembershipId == nil || mem.MembershipType == nil {
				continue
			}
			memberships = append(memberships, domain.Membership{
				ID:          *mem.MembershipId,
				Type:        int64(*mem.MembershipType),
				DisplayName: ptr.Deref(mem.DisplayName),
			})
		}
		account.DestinyMemberships = memberships
	}

	primaryID := account.PrimaryMembershipID
	if primaryID == "" {
		primaryID = "<none>"
	}
	clog.WithGroupFields(ctx, "bungieOAuth.getCurrentAccount", clog.Fields{
		"hasBungieNetUser":      data.BungieNetUser != nil,
		"primaryMembershipId":   primaryID,
		"numDestinyMemberships": len(account.DestinyMemberships),
	})
	return account, nil
}

func (g *OAuthGateway) HasAccess(ctx context.Context, membershipID, accessToken string) (bool, error) {
	clog.WithGroupField(ctx, "bungieOAuth.hasAccess", "targetMembershipId", membershipID)
	resp, err := g.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return nil
	})
	if err != nil {
		return false, err
	}
	if resp == nil || resp.JSON200 == nil || resp.JSON200.MembershipData == nil || resp.JSON200.MembershipData.BungieNetUser == nil || resp.JSON200.MembershipData.BungieNetUser.MembershipId == nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode()
		}
		clog.WithGroupFields(ctx, "bungieOAuth.hasAccess", clog.Fields{
			"statusCode": statusCode,
			"warning":    "incomplete bungie user data",
		})
		return false, nil
	}

	hasAccess := *resp.JSON200.MembershipData.BungieNetUser.MembershipId == membershipID
	clog.WithGroupField(ctx, "bungieOAuth.hasAccess", "hasAccess", hasAccess)
	return hasAccess, nil
}
