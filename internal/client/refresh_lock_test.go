package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

type rotatingTokens struct {
	mu         sync.Mutex
	generation int
	revoked    bool
	tokenCalls int
	reused     []string
}

func (r *rotatingTokens) refreshToken(gen int) string { return fmt.Sprintf("ref-%d", gen) }

func (r *rotatingTokens) accessToken(gen int) string { return fmt.Sprintf("acc-%d", gen) }

func (r *rotatingTokens) handler(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch req.URL.Path {
	case "/oauth/token":
		r.tokenCalls++
		_ = req.ParseForm()
		presented := req.Form.Get("refresh_token")
		if r.revoked || presented != r.refreshToken(r.generation) {
			r.revoked = true
			r.reused = append(r.reused, presented)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		r.generation++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  r.accessToken(r.generation),
			"refresh_token": r.refreshToken(r.generation),
			"expires_in":    900,
		})
	default:
		if req.Header.Get("Authorization") != "Bearer "+r.accessToken(r.generation) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (r *rotatingTokens) snapshot() (int, bool, int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation, r.revoked, r.tokenCalls, append([]string(nil), r.reused...)
}

func storeLogin(t *testing.T, cfg config.Config) {
	t.Helper()
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)
	t.Cleanup(func() { testutil.ClearIsolatedConfigDir(t, dir) })
	if err := cfg.Save(); err != nil {
		t.Fatalf("storing the login: %v", err)
	}
}

func loadLogin(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading the stored login: %v", err)
	}
	return cfg
}

func newRotatingServer(t *testing.T) (*httptest.Server, *rotatingTokens) {
	t.Helper()
	tokens := &rotatingTokens{}
	srv := httptest.NewServer(http.HandlerFunc(tokens.handler))
	t.Cleanup(srv.Close)
	return srv, tokens
}

func newStoredClient(t *testing.T, srv *httptest.Server) *client.Client {
	t.Helper()
	return client.New(loadLogin(t), srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
}

func getProducts(c *client.Client) (int, error) {
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func TestRefresh_SecondProcessAdoptsTheLoginTheFirstRotated(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(-time.Minute)})

	first := newStoredClient(t, srv)
	second := newStoredClient(t, srv)

	if status, err := getProducts(first); err != nil || status != http.StatusOK {
		t.Fatalf("first process: status %d err %v", status, err)
	}
	if status, err := getProducts(second); err != nil || status != http.StatusOK {
		t.Fatalf("second process holding the pre-rotation token: status %d err %v", status, err)
	}

	generation, revoked, calls, reused := tokens.snapshot()
	if calls != 1 || generation != 1 || revoked {
		t.Fatalf("token endpoint: %d calls, generation %d, revoked %v, reused %v — want one rotation and no reuse", calls, generation, revoked, reused)
	}
	stored := loadLogin(t)
	if stored.AccessToken != "acc-1" || stored.RefreshToken != "ref-1" {
		t.Errorf("stored login = %q/%q, want acc-1/ref-1", stored.AccessToken, stored.RefreshToken)
	}
}

func TestRefresh_ConcurrentProcessesRotateTheTokenOnce(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(-time.Minute)})

	const processes = 8
	clients := make([]*client.Client, processes)
	for i := range clients {
		clients[i] = newStoredClient(t, srv)
	}
	start := make(chan struct{})
	errs := make(chan error, processes)
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *client.Client) {
			defer wg.Done()
			<-start
			status, err := getProducts(c)
			if err == nil && status != http.StatusOK {
				err = fmt.Errorf("status %d", status)
			}
			errs <- err
		}(c)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a concurrent process failed: %v", err)
		}
	}

	generation, revoked, calls, reused := tokens.snapshot()
	if calls != 1 || generation != 1 || revoked {
		t.Fatalf("token endpoint: %d calls, generation %d, revoked %v, reused %v — want exactly one rotation for %d processes", calls, generation, revoked, reused, processes)
	}
}

func TestRefresh_AfterA401AdoptsAFresherStoredToken(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(time.Hour)})
	stale := newStoredClient(t, srv)

	rotatorView := loadLogin(t)
	rotatorView.ExpiresAt = time.Now().Add(-time.Minute)
	rotator := client.New(rotatorView, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	if status, err := getProducts(rotator); err != nil || status != http.StatusOK {
		t.Fatalf("rotating process: status %d err %v", status, err)
	}

	if status, err := getProducts(stale); err != nil || status != http.StatusOK {
		t.Fatalf("process whose access token went stale: status %d err %v", status, err)
	}
	generation, revoked, calls, reused := tokens.snapshot()
	if calls != 1 || generation != 1 || revoked {
		t.Fatalf("token endpoint: %d calls, generation %d, revoked %v, reused %v — the 401 must adopt the stored token, not refresh again", calls, generation, revoked, reused)
	}
}

func TestRefresh_RefusesWhenTheStoredLoginMovedToAnotherHost(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(-time.Minute)})
	stale := newStoredClient(t, srv)

	moved := config.Config{APIURL: "https://other.example.test", AccessToken: "other-acc", RefreshToken: "other-ref", ExpiresAt: time.Now().Add(time.Hour)}
	if err := moved.Save(); err != nil {
		t.Fatalf("storing the other login: %v", err)
	}

	_, err := getProducts(stale)
	if !errors.Is(err, client.ErrStoredLoginChanged) {
		t.Fatalf("refresh against a login stored for another host: got %v, want ErrStoredLoginChanged", err)
	}
	if _, _, calls, _ := tokens.snapshot(); calls != 0 {
		t.Errorf("token endpoint called %d times, want none", calls)
	}
	if stored := loadLogin(t); stored.APIURL != moved.APIURL || stored.RefreshToken != moved.RefreshToken {
		t.Errorf("the other host's login was overwritten: %+v", stored)
	}
}

func TestRefresh_RefusesAfterASignOutInsteadOfRestoringTheLogin(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(-time.Minute)})
	stale := newStoredClient(t, srv)

	signedOut := config.Config{APIURL: srv.URL}
	if err := signedOut.Save(); err != nil {
		t.Fatalf("signing out: %v", err)
	}

	_, err := getProducts(stale)
	if !errors.Is(err, client.ErrStoredLoginChanged) || !strings.Contains(err.Error(), "zensu auth login") {
		t.Fatalf("refresh after a sign-out: got %v, want ErrStoredLoginChanged naming zensu auth login", err)
	}
	if _, _, calls, _ := tokens.snapshot(); calls != 0 {
		t.Errorf("token endpoint called %d times, want none", calls)
	}
	if stored := loadLogin(t); stored.AccessToken != "" || stored.RefreshToken != "" {
		t.Errorf("a sign-out was undone by a stale process: %+v", stored)
	}
}

func TestRefresh_GivesUpWhileAnotherProcessHoldsTheLock(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(-time.Minute)})
	stale := newStoredClient(t, srv)

	unlock, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = stale.Do(ctx, http.MethodGet, "/api/products", nil)
	if !errors.Is(err, config.ErrSessionLockBusy) {
		t.Fatalf("refresh while another process holds the lock: got %v, want ErrSessionLockBusy", err)
	}
	if _, _, calls, _ := tokens.snapshot(); calls != 0 {
		t.Errorf("token endpoint called %d times while the lock was held, want none", calls)
	}
}

func TestRefresh_ResolvesTheTokenEndpointOnlyWhenItRefreshes(t *testing.T) {
	srv, tokens := newRotatingServer(t)
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "acc-0", RefreshToken: "ref-0", ExpiresAt: time.Now().Add(time.Hour)})

	resolved := 0
	resolver := client.WithTokenURLResolver(func(context.Context) string {
		resolved++
		return srv.URL + "/oauth/token"
	})
	valid := client.New(loadLogin(t), srv.URL, "", client.WithHTTPClient(srv.Client()), resolver)
	if status, err := getProducts(valid); err != nil || status != http.StatusOK {
		t.Fatalf("valid token: status %d err %v", status, err)
	}
	if resolved != 0 {
		t.Fatalf("a request with a valid token resolved the token endpoint %d times, want none", resolved)
	}

	expired := loadLogin(t)
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	if err := expired.Save(); err != nil {
		t.Fatalf("expiring the stored login: %v", err)
	}
	refreshing := client.New(loadLogin(t), srv.URL, "", client.WithHTTPClient(srv.Client()), resolver)
	if status, err := getProducts(refreshing); err != nil || status != http.StatusOK {
		t.Fatalf("expired token: status %d err %v", status, err)
	}
	if resolved != 1 {
		t.Errorf("a refresh resolved the token endpoint %d times, want once", resolved)
	}
	if _, _, calls, _ := tokens.snapshot(); calls != 1 {
		t.Errorf("token endpoint called %d times, want once", calls)
	}
}
