package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

type pageResponder func(page, perPage int) (int, any)

type pagedServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []url.Values
}

func newPagedServer(t *testing.T, path string, respond pageResponder) *pagedServer {
	t.Helper()
	ps := &pagedServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("got %s %s, want GET %s", r.Method, r.URL.Path, path)
		}
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		perPage, _ := strconv.Atoi(q.Get("per_page"))
		ps.mu.Lock()
		ps.queries = append(ps.queries, q)
		status, body := respond(page, perPage)
		ps.mu.Unlock()
		w.WriteHeader(status)
		if s, ok := body.(string); ok {
			_, _ = w.Write([]byte(s))
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pagedServer) requestedQueries() []url.Values {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return slices.Clone(ps.queries)
}

func (ps *pagedServer) requestedPages() []int {
	var pages []int
	for _, q := range ps.requestedQueries() {
		page, _ := strconv.Atoi(q.Get("page"))
		pages = append(pages, page)
	}
	return pages
}

func pageBody(data []map[string]any, total, page, perPage int) map[string]any {
	if data == nil {
		data = []map[string]any{}
	}
	return map[string]any{"data": data, "total": total, "page": page, "perPage": perPage}
}

func backendPages(items []map[string]any, maxPerPage int) pageResponder {
	return func(page, perPage int) (int, any) {
		if page < 1 {
			page = 1
		}
		if perPage < 1 {
			perPage = 20
		}
		perPage = min(perPage, maxPerPage)
		start := min((page-1)*perPage, len(items))
		end := min(start+perPage, len(items))
		total := 0
		if end > start {
			total = len(items)
		}
		return http.StatusOK, pageBody(items[start:end], total, page, perPage)
	}
}

func listFixture(n int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"id":         fmt.Sprintf("id-%03d", i),
			"slug":       fmt.Sprintf("slug-%03d", i),
			"title":      fmt.Sprintf("title-%03d", i),
			"step_order": i + 1,
			"tier_order": i + 1,
			"mock_type":  "image",
			"file_name":  fmt.Sprintf("file-%03d.png", i),
		}
	}
	return items
}

func assertPages(t *testing.T, srv *pagedServer, want []int) {
	t.Helper()
	if got := srv.requestedPages(); !slices.Equal(got, want) {
		t.Errorf("requested pages %v, want %v", got, want)
	}
	for _, q := range srv.requestedQueries() {
		if got := q.Get("per_page"); got != strconv.Itoa(listPageSize) {
			t.Errorf("page %s asked for per_page=%q, want %d", q.Get("page"), got, listPageSize)
		}
	}
}

var paginatedListCommands = []struct {
	name   string
	newCmd func(*Factory) *cobra.Command
	args   []string
	path   string
	marker string
}{
	{"features list", NewFeaturesCmd, []string{"list", "--product", "p1"}, "/api/features", "slug-"},
	{"products list", NewProductsCmd, []string{"list"}, "/api/products", "id-"},
	{"journeys list", NewJourneysCmd, []string{"list", "--product", "p1"}, "/api/products/p1/journeys", "slug-"},
	{"journeys steps", NewJourneysCmd, []string{"steps", "j1", "--product", "p1"}, "/api/products/p1/journeys/j1/steps", "title-"},
	{"subfeatures list", NewSubfeaturesCmd, []string{"list", "f1"}, "/api/features/f1/subfeatures", "slug-"},
	{"tiers list", NewTiersCmd, []string{"list", "--product", "p1"}, "/api/products/p1/tiers", "slug-"},
	{"roadmap list", NewRoadmapCmd, []string{"list", "--product", "p1"}, "/api/products/p1/roadmaps", "id-"},
	{"mocks list", NewMocksCmd, []string{"list", "f1"}, "/api/features/f1/mocks", "id-"},
}

func TestListCommands_TableShowsEveryPage(t *testing.T) {
	sizes := []struct {
		name       string
		items      int
		maxPerPage int
		wantPages  []int
	}{
		{"45 items in 20-item server pages", 45, 20, []int{1, 2, 3}},
		{"45 items in one 100-item page", 45, 100, []int{1}},
		{"245 items in 100-item pages", 245, 100, []int{1, 2, 3}},
	}
	for _, lc := range paginatedListCommands {
		for _, size := range sizes {
			t.Run(lc.name+"/"+size.name, func(t *testing.T) {
				srv := newPagedServer(t, lc.path, backendPages(listFixture(size.items), size.maxPerPage))
				f, out := testFactory(srv.Server)
				if err := runCmd(t, lc.newCmd(f), lc.args...); err != nil {
					t.Fatalf("%s error: %v", lc.name, err)
				}
				got := out.String()
				var wrong []string
				for i := 0; i < size.items; i++ {
					marker := fmt.Sprintf("%s%03d", lc.marker, i)
					if strings.Count(got, marker) != 1 {
						wrong = append(wrong, marker)
					}
				}
				if len(wrong) > 0 {
					t.Errorf("%d of %d items missing or repeated in the table, first %s, in:\n%s", len(wrong), size.items, wrong[0], got)
				}
				assertPages(t, srv, size.wantPages)
			})
		}
	}
}

func TestListCommands_JSONMergesEveryPageIntoOneEnvelope(t *testing.T) {
	const items = 245
	for _, lc := range paginatedListCommands {
		t.Run(lc.name, func(t *testing.T) {
			srv := newPagedServer(t, lc.path, backendPages(listFixture(items), 100))
			f, out := testFactory(srv.Server)
			if err := runCmd(t, lc.newCmd(f), append(slices.Clone(lc.args), "--json")...); err != nil {
				t.Fatalf("%s --json error: %v", lc.name, err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &keys); err != nil {
				t.Fatalf("--json output is not one JSON object: %v\n%s", err, out.String())
			}
			for _, key := range []string{"data", "total", "page", "perPage"} {
				if _, ok := keys[key]; !ok {
					t.Errorf("envelope is missing %q", key)
				}
			}
			if len(keys) != 4 {
				t.Errorf("envelope keys: got %d, want exactly data, total, page, perPage", len(keys))
			}
			var env struct {
				Data    []map[string]any `json:"data"`
				Total   int              `json:"total"`
				Page    int              `json:"page"`
				PerPage int              `json:"perPage"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if len(env.Data) != items || env.Total != items || env.Page != 1 || env.PerPage != items {
				t.Fatalf("envelope: got %d items, total %d, page %d, perPage %d; want %d items, total %d, page 1, perPage %d",
					len(env.Data), env.Total, env.Page, env.PerPage, items, items, items)
			}
			for i, item := range env.Data {
				if want := fmt.Sprintf("id-%03d", i); item["id"] != want {
					t.Fatalf("item %d: got id %v, want %s in server order", i, item["id"], want)
				}
			}
			assertPages(t, srv, []int{1, 2, 3})
		})
	}
}

func TestListCommands_KeepFiltersOnEveryPage(t *testing.T) {
	tests := []struct {
		name   string
		newCmd func(*Factory) *cobra.Command
		args   []string
		path   string
		want   url.Values
	}{
		{"features list", NewFeaturesCmd, []string{"list", "--product", "p1"}, "/api/features", url.Values{"productId": {"p1"}}},
		{"subfeatures list --compact", NewSubfeaturesCmd, []string{"list", "f1", "--compact"}, "/api/features/f1/subfeatures", url.Values{"view": {"compact"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPagedServer(t, tc.path, backendPages(listFixture(150), 100))
			f, _ := testFactory(srv.Server)
			if err := runCmd(t, tc.newCmd(f), tc.args...); err != nil {
				t.Fatalf("%s error: %v", tc.name, err)
			}
			for _, q := range srv.requestedQueries() {
				for key := range tc.want {
					if q.Get(key) != tc.want.Get(key) {
						t.Errorf("page %s: %s=%q, want %q", q.Get("page"), key, q.Get(key), tc.want.Get(key))
					}
				}
			}
			assertPages(t, srv, []int{1, 2})
		})
	}
}

func overlappingPages(page, _ int) (int, any) {
	items := listFixture(150)
	switch page {
	case 1:
		return http.StatusOK, pageBody(items[:100], 150, 1, 100)
	case 2:
		return http.StatusOK, pageBody(items[95:145], 150, 2, 100)
	}
	return http.StatusOK, pageBody(nil, 0, page, 100)
}

func TestFeaturesList_PaginationEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		respond   func() pageResponder
		wantItems int
		wantErr   []string
		wantPages []int
	}{
		{
			name:      "an exact multiple of the page size needs no extra request",
			respond:   func() pageResponder { return backendPages(listFixture(200), 100) },
			wantItems: 200,
			wantPages: []int{1, 2},
		},
		{
			name:      "an empty list is a single request",
			respond:   func() pageResponder { return backendPages(listFixture(0), 100) },
			wantItems: 0,
			wantPages: []int{1},
		},
		{
			name: "an error on a later page surfaces as an error",
			respond: func() pageResponder {
				pages := backendPages(listFixture(150), 100)
				return func(page, perPage int) (int, any) {
					if page == 2 {
						return http.StatusInternalServerError, map[string]any{"code": "internal_error", "message": "database unavailable"}
					}
					return pages(page, perPage)
				}
			},
			wantErr:   []string{"reading page 2 of the features list", "database unavailable (status 500)"},
			wantPages: []int{1, 2},
		},
		{
			name: "an undecodable later page is an error",
			respond: func() pageResponder {
				pages := backendPages(listFixture(150), 100)
				return func(page, perPage int) (int, any) {
					if page == 2 {
						return http.StatusOK, "not json"
					}
					return pages(page, perPage)
				}
			},
			wantErr:   []string{"page 2 of the features list is not a list response"},
			wantPages: []int{1, 2},
		},
		{
			name: "a server that ignores the page parameter cannot loop forever",
			respond: func() pageResponder {
				pages := backendPages(listFixture(45), 20)
				return func(_, perPage int) (int, any) { return pages(1, perPage) }
			},
			wantErr:   []string{"incomplete list", "received 20 of 45 features"},
			wantPages: []int{1, 2, 1, 2},
		},
		{
			name:      "pages that overlap and skip items are reported after one retry",
			respond:   func() pageResponder { return overlappingPages },
			wantErr:   []string{"incomplete list", "received 145 of 150 features", "run the command again"},
			wantPages: []int{1, 2, 3, 1, 2, 3},
		},
		{
			name: "a total that the pages never reach is reported",
			respond: func() pageResponder {
				pages := backendPages(listFixture(45), 100)
				return func(page, perPage int) (int, any) {
					status, body := pages(page, perPage)
					if env := body.(map[string]any); env["total"] != 0 {
						env["total"] = 50
					}
					return status, body
				}
			},
			wantErr:   []string{"incomplete list", "received 45 of 50 features"},
			wantPages: []int{1, 2, 1, 2},
		},
		{
			name: "a transient overlap is healed by the retry",
			respond: func() pageResponder {
				consistent := backendPages(listFixture(150), 100)
				firstPass := true
				return func(page, perPage int) (int, any) {
					if !firstPass {
						return consistent(page, perPage)
					}
					if page == 3 {
						firstPass = false
					}
					return overlappingPages(page, perPage)
				}
			},
			wantItems: 150,
			wantPages: []int{1, 2, 3, 1, 2},
		},
		{
			name: "the same item on two pages is listed once",
			respond: func() pageResponder {
				items := listFixture(150)
				return func(page, _ int) (int, any) {
					switch page {
					case 1:
						return http.StatusOK, pageBody(items[:100], 149, 1, 100)
					case 2:
						return http.StatusOK, pageBody(items[99:150], 150, 2, 100)
					}
					return http.StatusOK, pageBody(nil, 0, page, 100)
				}
			},
			wantItems: 150,
			wantPages: []int{1, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPagedServer(t, "/api/features", tc.respond())
			f, out := testFactory(srv.Server)
			err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--json")
			assertPages(t, srv, tc.wantPages)
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("want an error containing %q, got none; output:\n%s", tc.wantErr, out.String())
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				if out.Len() != 0 {
					t.Errorf("a failed listing must not print a partial list, got:\n%s", out.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("features list --json error: %v", err)
			}
			var env struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v\n%s", err, out.String())
			}
			ids := map[string]bool{}
			for _, item := range env.Data {
				ids[item.ID] = true
			}
			if len(env.Data) != tc.wantItems || len(ids) != tc.wantItems || env.Total != tc.wantItems {
				t.Errorf("got %d items (%d distinct, total %d), want %d", len(env.Data), len(ids), env.Total, tc.wantItems)
			}
		})
	}
}

func TestFeaturesList_StopsAtThePageCap(t *testing.T) {
	srv := newPagedServer(t, "/api/features", func(page, _ int) (int, any) {
		return http.StatusOK, pageBody([]map[string]any{{"id": fmt.Sprintf("endless-%d", page)}}, 1_000_000, page, 1)
	})
	f, out := testFactory(srv.Server)
	err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("did not end after %d pages", maxListPages)) {
		t.Fatalf("an endless list must stop at the page cap with an error, got: %v", err)
	}
	if pages := srv.requestedPages(); len(pages) != maxListPages || pages[len(pages)-1] != maxListPages {
		t.Errorf("want exactly %d page requests, got %d", maxListPages, len(pages))
	}
	if out.Len() != 0 {
		t.Errorf("a capped listing must not print a partial list, got %d bytes", out.Len())
	}
}

func TestListAll_ReusesOneClientForEveryPage(t *testing.T) {
	srv := newPagedServer(t, "/api/features", backendPages(listFixture(245), 100))
	f, _ := testFactory(srv.Server)
	newClient := f.NewClient
	calls := 0
	f.NewClient = func(ctx context.Context) (*client.Client, error) {
		calls++
		return newClient(ctx)
	}
	if err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1"); err != nil {
		t.Fatalf("features list error: %v", err)
	}
	if calls != 1 {
		t.Errorf("one listing must build one client, so endpoint discovery runs once; built %d", calls)
	}
	assertPages(t, srv, []int{1, 2, 3})
}

func TestListCommands_FirstPageErrorIsUnchanged(t *testing.T) {
	srv := newPagedServer(t, "/api/features", func(int, int) (int, any) {
		return http.StatusUnauthorized, map[string]any{"code": "unauthorized", "message": "token expired"}
	})
	f, _ := testFactory(srv.Server)
	err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1")
	if err == nil || err.Error() != "token expired (status 401)" {
		t.Fatalf("a first-page error must reach the user unwrapped, got: %v", err)
	}
	assertPages(t, srv, []int{1})
}
