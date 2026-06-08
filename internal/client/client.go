package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	tokenSkew      = 30 * time.Second
	defaultTimeout = 30 * time.Second
)

type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
	}
	return fmt.Sprintf("request failed with status %d", e.StatusCode)
}

type Client struct {
	BaseURL    string
	TokenURL   string
	HTTPClient *http.Client
	cfg        *config.Config
	now        func() time.Time
	save       func(*config.Config) error
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.HTTPClient = h } }

func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

func WithSaver(save func(*config.Config) error) Option { return func(c *Client) { c.save = save } }

func New(cfg *config.Config, baseURL, tokenURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL:    baseURL,
		TokenURL:   tokenURL,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		cfg:        cfg,
		now:        time.Now,
		save:       func(cf *config.Config) error { return cf.Save() },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	if c.usingBearer() && c.tokenExpired() {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
	}

	resp, err := c.send(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.cfg.APIKey == "" && c.cfg.RefreshToken != "" {
		resp.Body.Close()
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
		return c.send(ctx, method, path, body)
	}
	return resp, nil
}

func (c *Client) usingBearer() bool {
	return c.cfg.APIKey == "" && c.cfg.AccessToken != ""
}

func (c *Client) tokenExpired() bool {
	if c.cfg.ExpiresAt.IsZero() {
		return false
	}
	return !c.now().Before(c.cfg.ExpiresAt.Add(-tokenSkew))
}

func (c *Client) refresh(ctx context.Context) error {
	if c.cfg.RefreshToken == "" {
		return fmt.Errorf("session expired and no refresh token available — run `zensu auth login`")
	}
	tok, err := auth.RefreshToken(ctx, c.HTTPClient, c.TokenURL, c.cfg.RefreshToken)
	if err != nil {
		return fmt.Errorf("refreshing session: %w", err)
	}
	c.cfg.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.cfg.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		c.cfg.ExpiresAt = c.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return c.save(c.cfg)
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	switch {
	case c.cfg.APIKey != "":
		req.Header.Set("X-API-Key", c.cfg.APIKey)
	case c.cfg.AccessToken != "":
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTPClient.Do(req)
}

func CheckResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	apiErr := &APIError{StatusCode: resp.StatusCode}
	if err := json.Unmarshal(body, apiErr); err != nil || apiErr.Message == "" {
		apiErr.Message = string(body)
	}
	return apiErr
}
