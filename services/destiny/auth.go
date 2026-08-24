package destiny

import (
	"encoding/base64"
	"fmt"
	"github.com/go-resty/resty/v2"
	"golang.org/x/net/context"
	"log/slog"
	"net/http"
	"net/url"
	"oneTrick/clients/bungie"
)

type AuthService interface {
	GetAccessToken(context context.Context, code string) (*AuthResponse, error)
	RefreshAccessToken(refreshToken string) (*AuthResponse, error)
	GetCurrentUser(ctx context.Context, token string) (*bungie.MembershipData, error)
	HasAccess(ctx context.Context, membershipID, token string) (bool, error)
}

var _ AuthService = (*AuthServiceImpl)(nil)

type AuthServiceImpl struct {
	http         *resty.Client
	clientID     string
	clientSecret string
	bungieClient *bungie.ClientWithResponses
}

func NewAuthService(client *resty.Client, bungie *bungie.ClientWithResponses, clientID, clientSecret string) *AuthServiceImpl {
	return &AuthServiceImpl{
		http:         client,
		clientID:     clientID,
		clientSecret: clientSecret,
		bungieClient: bungie,
	}
}

type AuthError struct {
	ErrorType    string `json:"error"`
	ErrorMessage string `json:"error_description"`
}

func (a AuthError) Error() string {
	return fmt.Sprintf("%s: %s", a.ErrorType, a.ErrorMessage)
}

func CodeSnippet(code string) string {
	if len(code) == 0 {
		return "<empty>"
	}
	if len(code) <= 8 {
		return "***"
	}
	return code[:4] + "..." + code[len(code)-4:]
}

func (a *AuthServiceImpl) GetAccessToken(context context.Context, code string) (*AuthResponse, error) {
	slog.Info("Requesting access token from Bungie OAuth", "codeLen", len(code), "codeSnippet", CodeSnippet(code))
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(a.clientID + ":" + a.clientSecret))
	response := &AuthResponse{}
	responseError := &AuthError{}

	values := url.Values{
		"grant_type": []string{"authorization_code"},
		"code":       []string{code},
	}
	resp, err := a.http.R().
		SetHeader("Authorization", fmt.Sprintf("Basic %s", encodedCredentials)).
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetResult(&response).
		SetError(&responseError).
		SetHeader("Response-Type", "application/json").
		SetFormDataFromValues(values).
		Post("https://www.bungie.net/Platform/App/OAuth/Token")

	if err != nil {
		slog.Error("Network error getting access token from Bungie", "error", err, "codeLen", len(code), "codeSnippet", CodeSnippet(code))
		return nil, err
	}
	if resp.IsError() {
		slog.Error("Bungie OAuth token exchange failed",
			"statusCode", resp.StatusCode(),
			"status", resp.Status(),
			"errorType", responseError.ErrorType,
			"errorDescription", responseError.ErrorMessage,
			"rawBody", resp.String(),
			"codeLen", len(code),
			"codeSnippet", CodeSnippet(code),
		)
		return nil, fmt.Errorf("error getting access token: %s", responseError.Error())
	}
	slog.Info("Successfully obtained access token from Bungie", "membershipID", response.MembershipID, "expiresIn", response.ExpiresIn)
	return response, nil
}

func (a *AuthServiceImpl) RefreshAccessToken(refreshToken string) (*AuthResponse, error) {
	slog.Info("Requesting access token refresh from Bungie OAuth", "refreshTokenLen", len(refreshToken))
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(a.clientID + ":" + a.clientSecret))
	response := &AuthResponse{}
	responseError := &AuthError{}
	values := url.Values{
		"grant_type":    []string{"refresh_token"},
		"refresh_token": []string{refreshToken},
	}
	resp, err := a.http.R().
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetHeader("Authorization", fmt.Sprintf("Basic %s", encodedCredentials)).
		SetHeader("Response-Type", "application/json").
		SetFormDataFromValues(values).
		SetResult(&response).
		SetError(&responseError).
		Post("https://www.bungie.net/Platform/App/OAuth/Token")
	if err != nil {
		slog.Error("Network error refreshing access token from Bungie", "error", err)
		return nil, err
	}

	if resp.IsError() {
		slog.Error("Bungie OAuth token refresh failed",
			"statusCode", resp.StatusCode(),
			"status", resp.Status(),
			"errorType", responseError.ErrorType,
			"errorDescription", responseError.ErrorMessage,
			"rawBody", resp.String(),
		)
		return nil, fmt.Errorf("error refreshing access token: %s", responseError.Error())
	}
	slog.Info("Successfully refreshed access token from Bungie", "membershipID", response.MembershipID, "expiresIn", response.ExpiresIn)
	return response, nil
}

func (a *AuthServiceImpl) GetCurrentUser(ctx context.Context, token string) (*bungie.MembershipData, error) {
	slog.Info("Fetching current user membership data from Bungie")
	resp, err := a.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(ctx context.Context, req *http.Request) error {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
		return nil
	})
	if err != nil {
		slog.Error("Failed to request current user membership data from Bungie", "error", err)
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		slog.Error("Bungie returned non-200 status fetching current user membership data", "statusCode", resp.StatusCode(), "status", resp.Status(), "rawBody", string(resp.Body))
		return nil, fmt.Errorf("bungie API error: status %d", resp.StatusCode())
	}
	if resp.JSON200 == nil || resp.JSON200.MembershipData == nil {
		slog.Error("Bungie returned nil membership data", "statusCode", resp.StatusCode(), "rawBody", string(resp.Body))
		return nil, fmt.Errorf("no membership data")
	}

	data := resp.JSON200.MembershipData
	numMemberships := 0
	if data.DestinyMemberships != nil {
		numMemberships = len(*data.DestinyMemberships)
	}
	primaryID := "<none>"
	if data.PrimaryMembershipId != nil {
		primaryID = *data.PrimaryMembershipId
	}
	hasBungieNetUser := data.BungieNetUser != nil

	slog.Info("Successfully fetched current user membership data from Bungie",
		"hasBungieNetUser", hasBungieNetUser,
		"primaryMembershipId", primaryID,
		"numDestinyMemberships", numMemberships,
	)
	return data, nil
}

func (a *AuthServiceImpl) HasAccess(ctx context.Context, membershipID, token string) (bool, error) {
	resp, err := a.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(ctx context.Context, req *http.Request) error {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
		return nil
	})
	if err != nil {
		slog.Error("Failed to check access with Bungie", "error", err, "targetMembershipID", membershipID)
		return false, err
	}
	if resp.JSON200 == nil || resp.JSON200.MembershipData == nil || resp.JSON200.MembershipData.BungieNetUser == nil {
		slog.Warn("Bungie user data incomplete while checking access", "statusCode", resp.StatusCode(), "targetMembershipID", membershipID)
		return false, nil
	}
	hasAccess := *resp.JSON200.MembershipData.BungieNetUser.MembershipId == membershipID
	slog.Info("Completed access check", "targetMembershipID", membershipID, "hasAccess", hasAccess)
	return hasAccess, nil
}
