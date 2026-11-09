package destiny

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"oneTrick/clients/bungie"

	clog "github.com/Shannor/canonical-logger"
	"github.com/go-resty/resty/v2"
)

type AuthService interface {
	GetAccessToken(ctx context.Context, code string) (*AuthResponse, error)
	RefreshAccessToken(ctx context.Context, refreshToken string) (*AuthResponse, error)
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

func (a *AuthServiceImpl) GetAccessToken(ctx context.Context, code string) (*AuthResponse, error) {
	clog.WithGroupFields(ctx, "authService.getAccessToken", clog.Fields{
		"codeLen":     len(code),
		"codeSnippet": CodeSnippet(code),
	})
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
		return nil, err
	}
	if resp.IsError() {
		clog.WithGroupFields(ctx, "authService.getAccessToken", clog.Fields{
			"statusCode":       resp.StatusCode(),
			"errorType":        responseError.ErrorType,
			"errorDescription": responseError.ErrorMessage,
		})
		return nil, fmt.Errorf("error getting access token: %s", responseError.Error())
	}
	clog.WithGroupFields(ctx, "authService.getAccessToken", clog.Fields{
		"membershipId": response.MembershipID,
		"expiresIn":    response.ExpiresIn,
	})
	return response, nil
}

func (a *AuthServiceImpl) RefreshAccessToken(ctx context.Context, refreshToken string) (*AuthResponse, error) {
	clog.WithGroupField(ctx, "authService.refreshAccessToken", "refreshTokenLen", len(refreshToken))
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
		return nil, err
	}

	if resp.IsError() {
		clog.WithGroupFields(ctx, "authService.refreshAccessToken", clog.Fields{
			"statusCode":       resp.StatusCode(),
			"errorType":        responseError.ErrorType,
			"errorDescription": responseError.ErrorMessage,
		})
		return nil, fmt.Errorf("error refreshing access token: %s", responseError.Error())
	}
	clog.WithGroupFields(ctx, "authService.refreshAccessToken", clog.Fields{
		"membershipId": response.MembershipID,
		"expiresIn":    response.ExpiresIn,
	})
	return response, nil
}

func (a *AuthServiceImpl) GetCurrentUser(ctx context.Context, token string) (*bungie.MembershipData, error) {
	resp, err := a.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(ctx context.Context, req *http.Request) error {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		clog.WithGroupField(ctx, "authService.getCurrentUser", "statusCode", resp.StatusCode())
		return nil, fmt.Errorf("bungie API error: status %d", resp.StatusCode())
	}
	if resp.JSON200 == nil || resp.JSON200.MembershipData == nil {
		clog.WithGroupField(ctx, "authService.getCurrentUser", "statusCode", resp.StatusCode())
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

	clog.WithGroupFields(ctx, "authService.getCurrentUser", clog.Fields{
		"hasBungieNetUser":      hasBungieNetUser,
		"primaryMembershipId":   primaryID,
		"numDestinyMemberships": numMemberships,
	})
	return data, nil
}

func (a *AuthServiceImpl) HasAccess(ctx context.Context, membershipID, token string) (bool, error) {
	clog.WithGroupField(ctx, "authService.hasAccess", "targetMembershipId", membershipID)
	resp, err := a.bungieClient.UserGetMembershipDataForCurrentUserWithResponse(ctx, func(ctx context.Context, req *http.Request) error {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
		return nil
	})
	if err != nil {
		return false, err
	}
	if resp.JSON200 == nil || resp.JSON200.MembershipData == nil || resp.JSON200.MembershipData.BungieNetUser == nil {
		clog.WithGroupFields(ctx, "authService.hasAccess", clog.Fields{
			"statusCode": resp.StatusCode(),
			"warning":    "incomplete bungie user data",
		})
		return false, nil
	}
	hasAccess := *resp.JSON200.MembershipData.BungieNetUser.MembershipId == membershipID
	clog.WithGroupField(ctx, "authService.hasAccess", "hasAccess", hasAccess)
	return hasAccess, nil
}
