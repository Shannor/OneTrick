package destiny

import (
	"encoding/base64"
	"fmt"
	"github.com/go-resty/resty/v2"
	"golang.org/x/net/context"
	"log/slog"
	"net/url"
)

type AuthService interface {
	GetAccessToken(context context.Context, code string) (*AuthResponse, error)
	RefreshAccessToken(refreshToken string) (*AuthResponse, error)
}

var _ AuthService = (*AuthServiceImpl)(nil)

type AuthServiceImpl struct {
	http         *resty.Client
	clientID     string
	clientSecret string
}

func NewAuthService(client *resty.Client, clientID, clientSecret string) *AuthServiceImpl {
	return &AuthServiceImpl{
		http:         client,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

type AuthError struct {
	ErrorType    string `json:"error"`
	ErrorMessage string `json:"error_description"`
}

func (a AuthError) Error() string {
	return fmt.Sprintf("%s: %s", a.ErrorType, a.ErrorMessage)
}

func (a *AuthServiceImpl) GetAccessToken(context context.Context, code string) (*AuthResponse, error) {
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(a.clientID + ":" + a.clientSecret))
	response := &AuthResponse{}
	responseError := &AuthError{}

	values := url.Values{
		"grant_type": []string{"authorization_code"},
		"code":       []string{code},
	}
	resp, err := a.http.R().
		SetHeader("Authorization", fmt.Sprintf("Basic %s", encodedCredentials)).
		SetHeader("Context-Type", "application/x-www-form-urlencoded").
		SetResult(&response).
		SetError(&responseError).
		SetHeader("Response-Type", "application/json").
		SetFormDataFromValues(values).
		Post("https://www.bungie.net/Platform/App/OAuth/Token")

	if err != nil {
		slog.With("error", err.Error()).Error("Error getting access token")
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error getting access token: %s ", responseError.Error())
	}
	return response, nil
}

func (a *AuthServiceImpl) RefreshAccessToken(refreshToken string) (*AuthResponse, error) {
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(a.clientID + ":" + a.clientSecret))
	response := &AuthResponse{}
	responseError := &AuthError{}
	values := url.Values{
		"grant_type":    []string{"refresh_token"},
		"refresh_token": []string{refreshToken},
	}
	resp, err := a.http.R().
		SetHeader("Context-Type", "application/x-www-form-urlencoded").
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
		return nil, fmt.Errorf("error getting access token: %s ", responseError.Error())
	}
	return response, nil
}
