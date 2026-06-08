package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const ClientID = "zensu-cli"

const DefaultScope = "mcp:read mcp:write"

type Endpoints struct {
	Authorization string
	Token         string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func DiscoverEndpoints(ctx context.Context, httpClient *http.Client, apiURL string) Endpoints {
	fallback := Endpoints{
		Authorization: strings.TrimRight(apiURL, "/") + "/oauth/authorize",
		Token:         strings.TrimRight(apiURL, "/") + "/oauth/token",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return fallback
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallback
	}
	var meta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return fallback
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return fallback
	}
	if !endpointTrusted(apiURL, meta.AuthorizationEndpoint) || !endpointTrusted(apiURL, meta.TokenEndpoint) {
		return fallback
	}
	return Endpoints{Authorization: meta.AuthorizationEndpoint, Token: meta.TokenEndpoint}
}

func endpointTrusted(apiURL, endpoint string) bool {
	a, err := url.Parse(apiURL)
	if err != nil {
		return false
	}
	e, err := url.Parse(endpoint)
	if err != nil || e.Host == "" {
		return false
	}
	if a.Scheme == "https" && e.Scheme != "https" {
		return false
	}
	return true
}

func AuthorizeURL(authzEndpoint, redirectURI, challenge, state, scope string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("scope", scope)
	sep := "?"
	if strings.Contains(authzEndpoint, "?") {
		sep = "&"
	}
	return authzEndpoint + sep + q.Encode()
}

func ExchangeCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, redirectURI, code, verifier string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", ClientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	return postToken(ctx, httpClient, tokenEndpoint, form)
}

func RefreshToken(ctx context.Context, httpClient *http.Client, tokenEndpoint, refreshToken string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", ClientID)
	form.Set("refresh_token", refreshToken)
	return postToken(ctx, httpClient, tokenEndpoint, form)
}

func postToken(ctx context.Context, httpClient *http.Client, tokenEndpoint string, form url.Values) (TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var oerr struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &oerr) == nil && oerr.Error != "" {
			return TokenResponse{}, fmt.Errorf("token endpoint: %s: %s", oerr.Error, oerr.ErrorDescription)
		}
		return TokenResponse{}, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return TokenResponse{}, fmt.Errorf("decoding token response: %w", err)
	}
	return tok, nil
}

func ValidateAPIKey(ctx context.Context, httpClient *http.Client, apiURL, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/api/products", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("api key rejected (status %d)", resp.StatusCode)
	}
	return nil
}
