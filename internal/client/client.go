package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	tokenSkew            = 30 * time.Second
	defaultTimeout       = 30 * time.Second
	defaultUploadTimeout = 5 * time.Minute
)

var ErrStoredLoginChanged = errors.New("the stored zensu login changed while this command ran — run it again, or `zensu auth login` if you signed out")

const jsonContentType = "application/json"

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
	BaseURL         string
	TokenURL        string
	HTTPClient      *http.Client
	UploadTimeout   time.Duration
	cfg             *config.Config
	now             func() time.Time
	store           sessionStore
	resolveTokenURL func(context.Context) string
}

type sessionStore struct {
	Load func() (*config.Config, error)
	Save func(*config.Config) error
	Lock func(context.Context) (func(), error)
}

const maxRedirects = 10

func defaultPortForScheme(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func normalizedPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return defaultPortForScheme(u.Scheme)
}

func atDefaultPort(u *url.URL) bool {
	return u.Port() == "" || u.Port() == defaultPortForScheme(u.Scheme)
}

func sameHostAndPort(a, b *url.URL) bool {
	if !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	if a.Scheme == b.Scheme {
		return normalizedPort(a) == normalizedPort(b)
	}
	return atDefaultPort(a) && atDefaultPort(b)
}

func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	if origin.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect that downgrades %s to %s", origin.Scheme, req.URL.Scheme)
	}
	if !sameHostAndPort(req.URL, origin) {
		return fmt.Errorf("refusing cross-host redirect to %s", req.URL.Host)
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", len(via))
	}
	return nil
}

func NewGuardedHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: refuseCrossHostRedirect}
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.HTTPClient = h } }

func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

func WithSaver(save func(*config.Config) error) Option {
	return func(c *Client) { c.store = sessionStore{Save: save} }
}

func WithTokenURLResolver(resolve func(context.Context) string) Option {
	return func(c *Client) { c.resolveTokenURL = resolve }
}

func WithUploadTimeout(d time.Duration) Option { return func(c *Client) { c.UploadTimeout = d } }

func New(cfg *config.Config, baseURL, tokenURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL:       baseURL,
		TokenURL:      tokenURL,
		HTTPClient:    &http.Client{Timeout: defaultTimeout},
		UploadTimeout: defaultUploadTimeout,
		cfg:           cfg,
		now:           time.Now,
		store: sessionStore{
			Load: config.Load,
			Save: func(cf *config.Config) error { return cf.Save() },
			Lock: config.LockSession,
		},
	}
	for _, o := range opts {
		o(c)
	}
	inner := c.HTTPClient.CheckRedirect
	guarded := *c.HTTPClient
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := refuseCrossHostRedirect(req, via); err != nil {
			return err
		}
		if inner != nil {
			return inner(req, via)
		}
		return nil
	}
	c.HTTPClient = &guarded
	return c
}

func (c *Client) WithTimeout(d time.Duration) *Client {
	h := *c.HTTPClient
	h.Timeout = d
	cp := *c
	cp.HTTPClient = &h
	return &cp
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.do(ctx, method, path, jsonContentType, body)
}

func (c *Client) DoWithContentType(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	if body != nil && contentType == "" {
		return nil, fmt.Errorf("content type is required for %s %s", method, path)
	}
	return c.do(ctx, method, path, contentType, body)
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	if c.usingBearer() && c.tokenExpired() {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
	}

	resp, err := c.send(ctx, method, path, contentType, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.cfg.APIKey == "" && c.cfg.RefreshToken != "" {
		resp.Body.Close()
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
		return c.send(ctx, method, path, contentType, body)
	}
	return resp, nil
}

func (c *Client) httpClientFor(contentType string) *http.Client {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		return c.HTTPClient
	}
	if c.UploadTimeout <= 0 || c.UploadTimeout == c.HTTPClient.Timeout {
		return c.HTTPClient
	}
	upload := *c.HTTPClient
	upload.Timeout = c.UploadTimeout
	return &upload
}

const SessionTokenPrefix = "zst_"

const (
	AuthModeNone         = "none"
	AuthModeAPIKey       = "api_key"
	AuthModeSessionToken = "session_token"
	AuthModeLogin        = "login"
)

func (c *Client) AuthMode() string {
	switch {
	case c.cfg.APIKey != "":
		return AuthModeAPIKey
	case strings.HasPrefix(c.cfg.AccessToken, SessionTokenPrefix):
		return AuthModeSessionToken
	case c.cfg.AccessToken != "":
		return AuthModeLogin
	}
	return AuthModeNone
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
	if c.store.Lock != nil {
		lockCtx, cancel := context.WithTimeout(ctx, config.SessionLockWait)
		unlock, err := c.store.Lock(lockCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("refreshing session: %w", err)
		}
		defer unlock()
	}
	if c.store.Load != nil {
		stored, err := c.store.Load()
		if err != nil {
			return fmt.Errorf("refreshing session: reading the stored login: %w", err)
		}
		adopted, err := c.adoptStoredSession(stored)
		if err != nil {
			return err
		}
		if adopted && !c.tokenExpired() {
			return nil
		}
	}
	tok, err := auth.RefreshToken(ctx, c.HTTPClient, c.tokenURL(ctx), c.cfg.RefreshToken)
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
	if email, org := auth.IdentityFromToken(tok.AccessToken); email != "" {
		c.cfg.SetIdentity(email, org)
	}
	return c.store.Save(c.cfg)
}

func (c *Client) adoptStoredSession(stored *config.Config) (bool, error) {
	if stored.AccessToken == c.cfg.AccessToken && stored.RefreshToken == c.cfg.RefreshToken {
		return false, nil
	}
	if stored.RefreshToken == "" || stored.APIURL != c.cfg.APIURL || stored.APIKey != c.cfg.APIKey {
		return false, ErrStoredLoginChanged
	}
	c.cfg.AccessToken = stored.AccessToken
	c.cfg.RefreshToken = stored.RefreshToken
	c.cfg.ExpiresAt = stored.ExpiresAt
	c.cfg.User = stored.User
	c.cfg.Org = stored.Org
	return true, nil
}

func (c *Client) tokenURL(ctx context.Context) string {
	if c.TokenURL == "" && c.resolveTokenURL != nil {
		c.TokenURL = c.resolveTokenURL(ctx)
	}
	return c.TokenURL
}

func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
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
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpClientFor(contentType).Do(req)
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
