package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
	Raw    string
	Auth   string
	APIKey string
}

type fakeReply struct {
	Status int
	Body   any
}

type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recordedRequest
	routes   map[string]func(recordedRequest) fakeReply
}

func newFakeAPI(t *testing.T, routes map[string]func(recordedRequest) fakeReply) (*httptest.Server, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{t: t, routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		req := recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Raw:    string(raw),
			Auth:   r.Header.Get("Authorization"),
			APIKey: r.Header.Get("X-API-Key"),
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.Body)
		}
		api.mu.Lock()
		api.requests = append(api.requests, req)
		handler, ok := api.routes[r.Method+" "+r.URL.Path]
		api.mu.Unlock()
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reply := handler(req)
		if reply.Status == 0 {
			reply.Status = http.StatusOK
		}
		switch b := reply.Body.(type) {
		case nil:
			w.WriteHeader(reply.Status)
		case string:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(reply.Status)
			_, _ = io.WriteString(w, b)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(reply.Status)
			_ = json.NewEncoder(w).Encode(b)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, api
}

func (a *fakeAPI) calls() []recordedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedRequest(nil), a.requests...)
}

func (a *fakeAPI) only(t *testing.T, method, path string) recordedRequest {
	t.Helper()
	var found []recordedRequest
	for _, r := range a.calls() {
		if r.Method == method && r.Path == path {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s %s called %d times, want 1 (all calls: %v)", method, path, len(found), a.summary())
	}
	return found[0]
}

func (a *fakeAPI) count(method, path string) int {
	n := 0
	for _, r := range a.calls() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

func (a *fakeAPI) summary() []string {
	var out []string
	for _, r := range a.calls() {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

func reply(status int, body any) func(recordedRequest) fakeReply {
	return func(recordedRequest) fakeReply { return fakeReply{Status: status, Body: body} }
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
