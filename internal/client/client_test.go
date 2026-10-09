package client_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestDo_InjectsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer acc-1" {
			t.Errorf("Authorization: got %q want Bearer acc-1", got)
		}
		if r.Header.Get("X-API-Key") != "" {
			t.Error("X-API-Key must not be set when using bearer token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "acc-1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", resp.StatusCode)
	}
}

func TestDo_InjectsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "zsk_k" {
			t.Errorf("X-API-Key: got %q want zsk_k", got)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization must not be set when using api key")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k", AccessToken: "ignored"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
}

func TestDo_RefreshesExpiredTokenBeforeRequest(t *testing.T) {
	var saved bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("refresh grant_type: got %q", r.Form.Get("grant_type"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-acc", "refresh_token": "new-ref", "expires_in": 900})
		case "/api/products":
			if got := r.Header.Get("Authorization"); got != "Bearer new-acc" {
				t.Errorf("expected refreshed bearer, got %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{AccessToken: "old-acc", RefreshToken: "old-ref", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { saved = true; return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if cfg.AccessToken != "new-acc" || cfg.RefreshToken != "new-ref" {
		t.Errorf("config not updated after refresh: %+v", cfg)
	}
	if !saved {
		t.Error("expected refreshed tokens to be persisted via saver")
	}
}

func TestDo_RefreshUpdatesIdentityFromJWT(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@zensu.dev","orgName":"Zensu"}`))
	jwt := "h." + payload + ".sig"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": jwt, "refresh_token": "new-ref", "expires_in": 900})
		case "/api/products":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{AccessToken: "old-acc", RefreshToken: "old-ref", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if cfg.User != "dev@zensu.dev" || cfg.Org != "Zensu" {
		t.Errorf("identity not updated after refresh: %+v", cfg)
	}
}

func TestDo_RefreshKeepsIdentityWhenOpaque(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "opaque-new", "refresh_token": "new-ref", "expires_in": 900})
		case "/api/products":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{User: "old@x", Org: "OldOrg", AccessToken: "old-acc", RefreshToken: "old-ref", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if cfg.User != "old@x" || cfg.Org != "OldOrg" {
		t.Errorf("opaque refresh wiped existing identity: %+v", cfg)
	}
}

func TestDo_RefreshKeepsOrgWhenJWTHasNoOrg(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"new@x"}`))
	jwt := "h." + payload + ".sig"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": jwt, "refresh_token": "new-ref", "expires_in": 900})
		case "/api/products":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{User: "old@x", Org: "OldOrg", AccessToken: "old-acc", RefreshToken: "old-ref", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if cfg.User != "new@x" {
		t.Errorf("email not updated on refresh: %q", cfg.User)
	}
	if cfg.Org != "OldOrg" {
		t.Errorf("org clobbered by JWT without orgName: %q", cfg.Org)
	}
}

func TestDo_RetriesOnce401ThenRefresh(t *testing.T) {
	var apiCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "r2", "expires_in": 900})
		case "/api/products":
			apiCalls++
			if r.Header.Get("Authorization") == "Bearer fresh" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after 401+refresh status: got %d want 200", resp.StatusCode)
	}
	if apiCalls != 2 {
		t.Errorf("expected exactly 2 api calls (401 then retry), got %d", apiCalls)
	}
	if cfg.AccessToken != "fresh" {
		t.Errorf("token not refreshed: %q", cfg.AccessToken)
	}
}

func TestCheckResponse_ParsesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "bad_request", "message": "missing name"})
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	err = client.CheckResponse(resp)
	if err == nil {
		t.Fatal("CheckResponse should error on 400")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *client.APIError, got %T", err)
	}
	if apiErr.StatusCode != 400 || apiErr.Code != "bad_request" || apiErr.Message != "missing name" {
		t.Errorf("APIError fields wrong: %+v", apiErr)
	}
}

func TestAPIError_Error(t *testing.T) {
	withMessage := (&client.APIError{StatusCode: 404, Code: "not_found", Message: "feature not found"}).Error()
	if withMessage != "feature not found (status 404)" {
		t.Errorf("APIError with message: got %q", withMessage)
	}
	withoutMessage := (&client.APIError{StatusCode: 500}).Error()
	if withoutMessage != "request failed with status 500" {
		t.Errorf("APIError without message: got %q", withoutMessage)
	}
}

func TestCheckResponse_PassesSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"m1"}`))
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodPost, "/api/features/f1/mocks", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	defer resp.Body.Close()
	if err := client.CheckResponse(resp); err != nil {
		t.Errorf("CheckResponse must accept a 2xx response, got: %v", err)
	}
}

func TestDoWithContentType_RejectsEmptyContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request may be sent without a declared content type")
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	_, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "", []byte("body"))
	if err == nil {
		t.Fatal("DoWithContentType must reject an empty content type")
	}
	if !strings.Contains(err.Error(), "content type is required") {
		t.Errorf("error should name the missing content type, got: %v", err)
	}
}

type deadlineRecorder struct {
	deadlines map[string]time.Duration
}

func (d *deadlineRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	var left time.Duration
	if dl, ok := r.Context().Deadline(); ok {
		left = time.Until(dl)
	}
	if d.deadlines == nil {
		d.deadlines = map[string]time.Duration{}
	}
	d.deadlines[r.Header.Get("Content-Type")] = left
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

func TestUploadsGetALongerDeadlineThanJSONCalls(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: rec}),
	)

	resp, err := c.Do(context.Background(), http.MethodPost, "/api/products", []byte(`{"name":"p"}`))
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()

	resp, err = c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()

	jsonLeft := rec.deadlines["application/json"]
	uploadLeft := rec.deadlines["multipart/form-data; boundary=b"]
	if jsonLeft <= 0 || uploadLeft <= 0 {
		t.Fatalf("both requests must carry a client deadline: json=%v upload=%v", jsonLeft, uploadLeft)
	}
	if jsonLeft > 30*time.Second {
		t.Errorf("a JSON call must keep the default 30s budget, got %v", jsonLeft)
	}
	if uploadLeft <= jsonLeft {
		t.Errorf("an upload must get more time than a JSON call: upload=%v json=%v", uploadLeft, jsonLeft)
	}
	if uploadLeft < 4*time.Minute {
		t.Errorf("an upload must get the 5m default budget, not the JSON one: got %v", uploadLeft)
	}

	resp, err = c.Do(context.Background(), http.MethodPost, "/api/products", []byte(`{"name":"q"}`))
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if after := rec.deadlines["application/json"]; after > 30*time.Second {
		t.Errorf("the upload budget must not leak into a later JSON call: got %v", after)
	}
}

func TestWithUploadTimeout_Overrides(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: rec}),
		client.WithUploadTimeout(30*time.Minute),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if left := rec.deadlines["multipart/form-data; boundary=b"]; left < 25*time.Minute {
		t.Errorf("WithUploadTimeout not honored: deadline in %v", left)
	}
}

func TestDo_SurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	srv.Close()

	if _, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil); err == nil {
		t.Fatal("Do should error when the host is unreachable")
	}
}

func TestDo_RejectsUnusableRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	_, err := c.Do(context.Background(), "BAD METHOD", "/api/products", nil)
	if err == nil {
		t.Fatal("Do should error on a request it cannot construct")
	}
	if !strings.Contains(err.Error(), "creating request") {
		t.Errorf("error should name the construction failure, got: %v", err)
	}
}

func TestDo_SurfacesRefreshFailureAfter401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	_, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil {
		t.Fatal("Do should error when the post-401 refresh fails")
	}
	if !strings.Contains(err.Error(), "refreshing session") {
		t.Errorf("error should name the failed refresh, got: %v", err)
	}
}

func TestDo_SendsJSONContentType(t *testing.T) {
	var gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodPost, "/api/products", []byte(`{"name":"p"}`))
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if gotType != "application/json" {
		t.Errorf("Content-Type: got %q want application/json", gotType)
	}
	if string(gotBody) != `{"name":"p"}` {
		t.Errorf("body: got %q", gotBody)
	}
}

func TestDoWithContentType_UsesCallerContentType(t *testing.T) {
	const boundaryType = "multipart/form-data; boundary=zensu-test"
	var gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", boundaryType, []byte("--zensu-test--\r\n"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status: got %d want 201", resp.StatusCode)
	}
	if gotType != boundaryType {
		t.Errorf("Content-Type: got %q want %q", gotType, boundaryType)
	}
	if string(gotBody) != "--zensu-test--\r\n" {
		t.Errorf("body: got %q", gotBody)
	}
}

func TestDoWithContentType_RetriesOn401KeepingContentTypeAndBody(t *testing.T) {
	const boundaryType = "multipart/form-data; boundary=zensu-test"
	var seenTypes []string
	var seenBodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "r2", "expires_in": 900})
		case "/api/features/f1/mocks":
			body, _ := io.ReadAll(r.Body)
			seenTypes = append(seenTypes, r.Header.Get("Content-Type"))
			seenBodies = append(seenBodies, string(body))
			if r.Header.Get("Authorization") == "Bearer fresh" {
				w.WriteHeader(http.StatusCreated)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", boundaryType, []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("after 401+refresh status: got %d want 201", resp.StatusCode)
	}
	if len(seenTypes) != 2 {
		t.Fatalf("expected exactly 2 upload attempts (401 then retry), got %d", len(seenTypes))
	}
	for i, got := range seenTypes {
		if got != boundaryType {
			t.Errorf("attempt %d Content-Type: got %q want %q", i+1, got, boundaryType)
		}
	}
	for i, got := range seenBodies {
		if got != "payload" {
			t.Errorf("attempt %d body: got %q want %q", i+1, got, "payload")
		}
	}
}

func TestNew_RefusesCrossHostRedirect(t *testing.T) {
	var elsewhere *httptest.Server
	elsewhere = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials must never reach a redirect target on another host")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/stolen", http.StatusFound)
	}))
	defer api.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, api.URL, api.URL+"/oauth/token")
	_, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil {
		t.Fatal("a cross-host redirect must be refused")
	}
	if !strings.Contains(err.Error(), "refusing cross-host redirect") {
		t.Errorf("error should name the refused redirect, got: %v", err)
	}
}

func TestNew_StopsAfterTooManyRedirects(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/api/products", http.StatusFound)
	}))
	defer srv.Close()

	bounded := srv.Client()
	bounded.Timeout = 2 * time.Second

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(bounded))
	_, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil {
		t.Fatal("a same-host redirect loop must be refused")
	}
	if hits != 10 {
		t.Errorf("the guard refuses at len(via) >= 10, so the server must be hit exactly 10 times, got %d", hits)
	}
	if !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Errorf("the error must name the hop limit exactly, so a changed cap cannot pass unnoticed, got: %v", err)
	}
}

func TestDoWithContentType_AllowsEmptyContentTypeWithoutBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("a bodyless request must not declare a content type, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.DoWithContentType(context.Background(), http.MethodGet, "/api/products", "", nil)
	if err != nil {
		t.Fatalf("a bodyless request needs no content type: %v", err)
	}
	resp.Body.Close()
}

func TestNew_AllowsSameHostRedirect(t *testing.T) {
	var hops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops = append(hops, r.URL.Path)
		if r.URL.Path == "/api/products" {
			http.Redirect(w, r, "/api/products/", http.StatusFound)
			return
		}
		if r.Header.Get("X-API-Key") != "zsk_k" {
			t.Errorf("the credential must survive a same-host redirect, got %q", r.Header.Get("X-API-Key"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token")
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("a same-host redirect must be followed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status after the redirect: got %d want 200", resp.StatusCode)
	}
	if len(hops) != 2 {
		t.Errorf("expected the redirect to be followed once, saw hops %v", hops)
	}
}

func TestNew_KeepsTheRedirectGuardWhenAClientIsInjected(t *testing.T) {
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{}),
	)
	if c.HTTPClient.CheckRedirect == nil {
		t.Fatal("an injected client must not be able to drop the redirect guard silently")
	}
	err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "evil.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	)
	if err == nil || !strings.Contains(err.Error(), "refusing cross-host redirect") {
		t.Errorf("the reinstated guard must refuse a cross-host redirect, got: %v", err)
	}
}

func TestNew_KeepsTheRedirectGuardWhenTheInjectedClientCarriesItsOwnPolicy(t *testing.T) {
	var innerCalled bool
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			innerCalled = true
			return nil
		}}),
	)
	err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "evil.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	)
	if err == nil || !strings.Contains(err.Error(), "refusing cross-host redirect") {
		t.Errorf("a caller policy must not be able to drop the guard: supplying any CheckRedirect also replaces the stdlib default, so the guard has to run first, got: %v", err)
	}
	if innerCalled {
		t.Error("the caller policy must not be consulted for a hop the guard already refused")
	}

	innerCalled = false
	if err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "zensu.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	); err != nil {
		t.Errorf("a hop the guard permits must still reach the caller policy, got: %v", err)
	}
	if !innerCalled {
		t.Error("the caller policy must still run for a hop the guard permits")
	}
}

func TestRefuseCrossHostRedirect_RefusesSchemeDowngrade(t *testing.T) {
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "https://zensu.test", "https://zensu.test/oauth/token")
	err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "http", Host: "zensu.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	)
	if err == nil || !strings.Contains(err.Error(), "downgrades") {
		t.Errorf("an https->http redirect on the same host must be refused, got: %v", err)
	}
}

func TestRefuseCrossHostRedirect_AllowsTheSameHostSpelledDifferently(t *testing.T) {
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "https://zensu.test", "https://zensu.test/oauth/token")
	for _, target := range []string{"zensu.test:443", "ZENSU.TEST"} {
		if err := c.HTTPClient.CheckRedirect(
			&http.Request{URL: &url.URL{Scheme: "https", Host: target}},
			[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
		); err != nil {
			t.Errorf("url.URL.Host carries the port and preserves case, so spelling out the default port or shifting case is refused as cross-host with a message that reads as nonsense; %q is the same origin, got: %v", target, err)
		}
	}
	if err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "evil.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	); err == nil {
		t.Error("loosening the comparison must not let a genuinely different host through")
	}

	plain := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token")
	if err := plain.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "http", Host: "zensu.test:80"}},
		[]*http.Request{{URL: &url.URL{Scheme: "http", Host: "zensu.test"}}},
	); err != nil {
		t.Errorf("the default port for http is 80, so spelling it out is the same origin, got: %v", err)
	}
	if err := plain.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "http", Host: "zensu.test:8080"}},
		[]*http.Request{{URL: &url.URL{Scheme: "http", Host: "zensu.test"}}},
	); err == nil {
		t.Error("a different port is a different origin and must stay refused; dropping the port entirely would make two services on one host indistinguishable")
	}
}

func TestHTTPClientFor_LeavesTheClientAloneWhenTheBudgetsMatch(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	base := &http.Client{Timeout: 30 * time.Second, Transport: rec}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(base),
		client.WithUploadTimeout(30*time.Second),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if got := rec.deadlines["multipart/form-data; boundary=b"]; got > 35*time.Second {
		t.Errorf("an upload budget equal to the base needs no second client and must not extend the deadline, got roughly %v", got.Round(time.Second))
	}
}

func TestRefuseCrossHostRedirect_AllowsAnUpgradeToHTTPSOnTheSameHost(t *testing.T) {
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token")
	if err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "zensu.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "http", Host: "zensu.test"}}},
	); err != nil {
		t.Errorf("a plain-http base URL that the server upgrades to https on the same host is the ordinary deployment shape; normalizing the default port made 80 and 443 differ and refused it as cross-host, got: %v", err)
	}
	if err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "evil.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "http", Host: "zensu.test"}}},
	); err == nil {
		t.Error("allowing the upgrade must not also allow it to a different host")
	}

	for _, pair := range [][2]string{{"zensu.test:80", "zensu.test"}, {"zensu.test", "zensu.test:443"}} {
		if err := c.HTTPClient.CheckRedirect(
			&http.Request{URL: &url.URL{Scheme: "https", Host: pair[1]}},
			[]*http.Request{{URL: &url.URL{Scheme: "http", Host: pair[0]}}},
		); err != nil {
			t.Errorf("comparing raw ports across differing schemes makes the upgrade work only when both sides spell the port the same way; %s to %s is the same host at each scheme's default, got: %v", pair[0], pair[1], err)
		}
	}
	if err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "zensu.test:8443"}},
		[]*http.Request{{URL: &url.URL{Scheme: "http", Host: "zensu.test"}}},
	); err == nil {
		t.Error("an upgrade to a non-default port is a different origin and must stay refused")
	}
}

func TestNew_HonorsTheInjectedPolicysVerdictForAPermittedHop(t *testing.T) {
	refused := errors.New("the caller policy said no")
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "https://zensu.test", "https://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return refused
		}}),
	)
	err := c.HTTPClient.CheckRedirect(
		&http.Request{URL: &url.URL{Scheme: "https", Host: "zensu.test"}},
		[]*http.Request{{URL: &url.URL{Scheme: "https", Host: "zensu.test"}}},
	)
	if !errors.Is(err, refused) {
		t.Errorf("calling the caller policy and discarding its answer would keep every other assertion in this file green while the injected policy became a no-op; its verdict must be returned, got: %v", err)
	}
}

func TestHTTPClientFor_HonorsAnUploadTimeoutShorterThanTheBase(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: rec}),
		client.WithUploadTimeout(5*time.Second),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if got := rec.deadlines["multipart/form-data; boundary=b"]; got > 10*time.Second {
		t.Errorf("the option is named for the upload timeout, not for a floor under it: a caller asking for 5s got roughly %v", got.Round(time.Second))
	}
}

func TestHTTPClientFor_GivesAnUploadADeadlineWhenTheBaseClientHasNone(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{Transport: rec}),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if rec.deadlines["multipart/form-data; boundary=b"] == 0 {
		t.Error("a zero base timeout means no deadline at all, which is exactly the case where an upload budget matters most; it must still receive UploadTimeout")
	}
}

func TestHTTPClientFor_MatchesTheMediaTypeExactly(t *testing.T) {
	rec := &deadlineRecorder{}
	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: rec}),
	)
	resp, err := c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-datax", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if got := rec.deadlines["multipart/form-datax"]; got > 60*time.Second {
		t.Errorf("a media type is a structured value, not a string prefix: multipart/form-datax is not an upload and must keep the base budget, got roughly %v", got.Round(time.Second))
	}

	resp, err = c.DoWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "Multipart/Form-Data; boundary=b", []byte("payload"))
	if err != nil {
		t.Fatalf("DoWithContentType error: %v", err)
	}
	resp.Body.Close()
	if got := rec.deadlines["Multipart/Form-Data; boundary=b"]; got <= 60*time.Second {
		t.Errorf("RFC 7231 media types are case-insensitive, so this is an upload and must get the longer budget, got roughly %v", got.Round(time.Second))
	}
}

func TestNewGuardedHTTPClient_RefusesACrossHostRedirect(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"token_endpoint":"https://attacker.example/oauth/token"}`)
	}))
	defer elsewhere.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/.well-known/oauth-authorization-server", http.StatusFound)
	}))
	defer origin.Close()

	c := client.NewGuardedHTTPClient(5 * time.Second)
	resp, err := c.Get(origin.URL + "/.well-known/oauth-authorization-server")
	if err == nil {
		resp.Body.Close()
		t.Fatal("discovery decides where the refresh token is sent, so a redirect off the named host must fail rather than be followed")
	}
	if !strings.Contains(err.Error(), "cross-host redirect") {
		t.Errorf("the refusal must name its reason, got: %v", err)
	}
}

func TestNewGuardedHTTPClient_KeepsTheRequestedTimeout(t *testing.T) {
	if got := client.NewGuardedHTTPClient(7 * time.Second).Timeout; got != 7*time.Second {
		t.Errorf("the caller's budget must survive the guard, got %v", got)
	}
}
