package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestWikiList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/wiki/pages" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "w1", "slug": "getting-started", "title": "Getting Started", "audience": "developer"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("wiki list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "SLUG", "TITLE", "AUDIENCE", "getting-started", "Getting Started", "developer"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

var wikiIDPattern = regexp.MustCompile(`\bw\d{3}\b`)

func wikiFixture(n int) []map[string]any {
	pages := make([]map[string]any, n)
	for i := range pages {
		pages[i] = map[string]any{"id": fmt.Sprintf("w%03d", i), "slug": fmt.Sprintf("page-%03d", i), "title": "T", "audience": "developer"}
	}
	return pages
}

func wikiIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("w%03d", i)
	}
	return ids
}

func legacyWikiArray(n int) pageResponder {
	pages := wikiFixture(n)
	return func(int, int) (int, any) { return http.StatusOK, pages }
}

func TestWikiList_ReadsEveryPageAndWarnsOnlyForTheLegacyArray(t *testing.T) {
	tests := []struct {
		name        string
		respond     pageResponder
		json        bool
		wantPages   []int
		wantIDs     int
		wantJSON    any
		wantWarning bool
		wantErr     string
	}{
		{
			name:      "an envelope list is read over three requests",
			respond:   backendPages(wikiFixture(250), 100),
			wantPages: []int{1, 2, 3},
			wantIDs:   250,
		},
		{
			name:      "--json prints every page as one envelope",
			respond:   backendPages(wikiFixture(250), 100),
			json:      true,
			wantPages: []int{1, 2, 3},
			wantIDs:   250,
			wantJSON:  map[string]any{"data": wikiFixture(250), "total": 250, "page": 1, "perPage": 250},
		},
		{
			name:      "an envelope list of exactly 50 pages is complete, so it carries no warning",
			respond:   backendPages(wikiFixture(legacyWikiPageLimit), 100),
			wantPages: []int{1},
			wantIDs:   legacyWikiPageLimit,
		},
		{
			name:      "an empty envelope list prints the header only",
			respond:   backendPages(wikiFixture(0), 100),
			wantPages: []int{1},
		},
		{
			name:      "a legacy array below the old limit carries no warning",
			respond:   legacyWikiArray(legacyWikiPageLimit - 1),
			wantPages: []int{1},
			wantIDs:   legacyWikiPageLimit - 1,
		},
		{
			name:        "a legacy array at the old limit warns",
			respond:     legacyWikiArray(legacyWikiPageLimit),
			wantPages:   []int{1},
			wantIDs:     legacyWikiPageLimit,
			wantWarning: true,
		},
		{
			name:        "a legacy array at the old limit prints unchanged with --json and warns",
			respond:     legacyWikiArray(legacyWikiPageLimit),
			json:        true,
			wantPages:   []int{1},
			wantIDs:     legacyWikiPageLimit,
			wantJSON:    wikiFixture(legacyWikiPageLimit),
			wantWarning: true,
		},
		{
			name:      "a legacy array above the old limit carries no warning",
			respond:   legacyWikiArray(legacyWikiPageLimit + 1),
			wantPages: []int{1},
			wantIDs:   legacyWikiPageLimit + 1,
		},
		{
			name: "an error status on the first page reaches the user unwrapped",
			respond: func(int, int) (int, any) {
				return http.StatusServiceUnavailable, map[string]any{"code": "service_unavailable", "message": "wiki service does not report a page total; update the wiki service"}
			},
			wantPages: []int{1},
			wantErr:   "wiki service does not report a page total; update the wiki service (status 503, code service_unavailable)",
		},
		{
			name: "an error status on a later page names that page",
			respond: func(page, perPage int) (int, any) {
				if page == 2 {
					return http.StatusInternalServerError, map[string]any{"code": "internal_error", "message": "database unavailable"}
				}
				return backendPages(wikiFixture(250), 100)(page, perPage)
			},
			wantPages: []int{1, 2},
			wantErr:   "reading page 2 of the wiki pages list: database unavailable (status 500, code internal_error)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPagedServer(t, "/api/wiki/pages", tc.respond)
			f, out := testFactory(srv.Server)
			cmd := NewWikiCmd(f)
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)
			args := []string{"list", "--product", "p1"}
			if tc.json {
				args = append(args, "--json")
			}
			err := runCmd(t, cmd, args...)
			assertPages(t, srv, tc.wantPages)
			if got := strings.Contains(stderr.String(), "at most 50 wiki pages per query"); got != tc.wantWarning {
				t.Errorf("limit warning on stderr: got %t, want %t (stderr %q)", got, tc.wantWarning, stderr.String())
			}
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error: got %v, want %q", err, tc.wantErr)
				}
				if out.Len() != 0 {
					t.Errorf("a failed listing must not print a partial list, got:\n%s", out.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("wiki list error: %v", err)
			}
			if strings.Contains(out.String(), "warning") {
				t.Errorf("the warning must not reach stdout:\n%s", out.String())
			}
			if got := wikiIDPattern.FindAllString(out.String(), -1); !slices.Equal(got, wikiIDs(tc.wantIDs)) {
				t.Errorf("stdout lists the ids %v, want each id once in server order: %v", got, wikiIDs(tc.wantIDs))
			}
			if !tc.json {
				lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
				if header := strings.Fields(lines[0]); !slices.Equal(header, []string{"ID", "SLUG", "TITLE", "AUDIENCE"}) {
					t.Errorf("the first line must be the table header, got %q", lines[0])
				}
				if len(lines)-1 != tc.wantIDs {
					t.Errorf("the table has %d rows, want %d", len(lines)-1, tc.wantIDs)
				}
				return
			}
			var got, want any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("--json output is not JSON: %v\n%s", err, out.String())
			}
			if err := json.Unmarshal([]byte(jsonOf(t, tc.wantJSON)), &want); err != nil {
				t.Fatalf("decode the expected JSON: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("--json output:\n%s\nwant the same JSON as:\n%s", out.String(), jsonOf(t, tc.wantJSON))
			}
		})
	}
}

func TestWikiList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without --product, which it would reject")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewWikiCmd(f), "list", "--audience", "developer")
	if err == nil || !strings.Contains(err.Error(), "--product is required") {
		t.Fatalf("wiki list without --product must fail before the request, got: %v", err)
	}
}

func TestWikiList_Filters(t *testing.T) {
	var q map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = map[string]string{
			"productId":    r.URL.Query().Get("productId"),
			"audience":     r.URL.Query().Get("audience"),
			"parentPageId": r.URL.Query().Get("parentPageId"),
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1", "--audience", "developer", "--parent", "pp1"); err != nil {
		t.Fatalf("wiki list error: %v", err)
	}
	if q["productId"] != "p1" {
		t.Errorf("productId query: got %q want p1", q["productId"])
	}
	if q["audience"] != "developer" {
		t.Errorf("audience query: got %q want developer", q["audience"])
	}
	if q["parentPageId"] != "pp1" {
		t.Errorf("parentPageId query: got %q want pp1", q["parentPageId"])
	}
}

func TestWikiCreate_SendsEveryCreateAsCreateOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{
			name: "a create without optional flags sends only the required fields",
			args: []string{"--product", "p1", "--title", "Guide", "--content", "# Hello"},
			want: map[string]any{"productId": "p1", "title": "Guide", "content": "# Hello", "createOnly": true},
		},
		{
			name: "a public create",
			args: []string{"--product", "p1", "--title", "Guide", "--content", "# Hello", "--audience", "developer", "--visibility", "public"},
			want: map[string]any{"productId": "p1", "title": "Guide", "content": "# Hello", "audience": "developer", "visibility": "public", "createOnly": true},
		},
		{
			name: "a feature-linked create",
			args: []string{"--product", "p1", "--title", "Guide", "--content", "# Hello", "--entity-type", "feature", "--entity-id", "f1", "--doc-type", "user_facing"},
			want: map[string]any{"productId": "p1", "title": "Guide", "content": "# Hello", "entityType": "feature", "entityId": "f1", "docType": "user_facing", "createOnly": true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"POST /api/wiki/pages": reply(http.StatusCreated, map[string]any{"id": "w9", "slug": "guide", "title": "Guide"}),
			})
			f, out := testFactory(srv)
			if err := runCmd(t, NewWikiCmd(f), append([]string{"create"}, tc.args...)...); err != nil {
				t.Fatalf("wiki create error: %v", err)
			}
			if calls := api.summary(); !slices.Equal(calls, []string{"POST /api/wiki/pages"}) {
				t.Fatalf("wiki create must send exactly one POST and never a PUT, got %v", calls)
			}
			if body := api.only(t, http.MethodPost, "/api/wiki/pages").Body; !reflect.DeepEqual(body, tc.want) {
				t.Errorf("create body = %#v\nwant          %#v", body, tc.want)
			}
			if want := "Created wiki page guide \"Guide\" (w9)\n"; out.String() != want {
				t.Errorf("stdout = %q, want %q", out.String(), want)
			}
		})
	}
}

func TestWikiCreate_NamesAnExistingPageAndLeavesItUnchanged(t *testing.T) {
	const pageID = "7d2f0c4e-1a3b-4c5d-8e9f-0a1b2c3d4e5f"
	exists := func(path string) string {
		return `{"code":"PAGE_EXISTS","message":"A page already exists at this path","pageId":"` + pageID + `","path":"` + path + `"}`
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		json   bool
		want   string
	}{
		{
			name:   "an unlinked page",
			status: http.StatusConflict,
			body:   exists("/custom/guide"),
			want:   `error: a wiki page already exists at /custom/guide (` + pageID + `) and was left unchanged; use "zensu wiki update ` + pageID + `" to change it (status 409)` + "\n",
		},
		{
			name:   "an unlinked page with --json",
			status: http.StatusConflict,
			body:   exists("/custom/guide"),
			json:   true,
			want: `error: a wiki page already exists at /custom/guide (` + pageID + `) and was left unchanged; use "zensu wiki update ` + pageID + `" to change it (status 409)` + "\n" +
				`{"error":{"status":409,"message":"a wiki page already exists at /custom/guide (` + pageID + `) and was left unchanged; use \"zensu wiki update ` + pageID + `\" to change it"}}` + "\n",
		},
		{
			name:   "a feature-linked page",
			status: http.StatusConflict,
			body:   exists("/features/checkout/user_facing"),
			want:   `error: a wiki page already exists at /features/checkout/user_facing (` + pageID + `) and was left unchanged; use "zensu wiki update ` + pageID + `" to change it (status 409)` + "\n",
		},
		{
			name:   "a path with terminal control sequences",
			status: http.StatusConflict,
			body:   exists(`/custom/gu\u001b[2Jide`),
			want:   `error: a wiki page already exists at /custom/gu[2Jide (` + pageID + `) and was left unchanged; use "zensu wiki update ` + pageID + `" to change it (status 409)` + "\n",
		},
		{
			name:   "an answer without a path still names the page",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_EXISTS","message":"A page already exists at this path","pageId":"` + pageID + `"}`,
			want:   `error: a wiki page already exists (` + pageID + `) and was left unchanged; use "zensu wiki update ` + pageID + `" to change it (status 409)` + "\n",
		},
		{
			name:   "PAGE_EXISTS without a pageId keeps the generic error",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_EXISTS","message":"A page already exists at this path","path":"/custom/guide"}`,
			want:   "error: A page already exists at this path (status 409)\n",
		},
		{
			name:   "PAGE_EXISTS without a pageId keeps the generic error with --json",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_EXISTS","message":"A page already exists at this path","path":"/custom/guide"}`,
			json:   true,
			want:   "error: A page already exists at this path (status 409)\n" + `{"error":{"status":409,"message":"A page already exists at this path"}}` + "\n",
		},
		{
			name:   "a pageId that is not a UUID keeps the generic error",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_EXISTS","message":"A page already exists at this path","pageId":"x\" && touch pwned #","path":"/custom/guide"}`,
			want:   "error: A page already exists at this path (status 409)\n",
		},
		{
			name:   "PAGE_EXISTS with another status keeps the generic error",
			status: http.StatusBadRequest,
			body:   exists("/custom/guide"),
			want:   "error: A page already exists at this path (status 400)\n",
		},
		{
			name:   "PAGE_CONFLICT keeps the generic error",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_CONFLICT","message":"Page changed during creation","pageId":"` + pageID + `","path":"/custom/guide"}`,
			want:   "error: Page changed during creation (status 409)\n",
		},
		{
			name:   "PAGE_CONFLICT keeps the generic error with --json",
			status: http.StatusConflict,
			body:   `{"code":"PAGE_CONFLICT","message":"Page changed during creation","pageId":"` + pageID + `","path":"/custom/guide"}`,
			json:   true,
			want:   "error: Page changed during creation (status 409)\n" + `{"error":{"status":409,"message":"Page changed during creation"}}` + "\n",
		},
		{
			name:   "a 409 without a JSON body keeps the generic error",
			status: http.StatusConflict,
			body:   "conflict",
			want:   "error: request failed (status 409): conflict\n",
		},
		{
			name:   "a 409 without a JSON body keeps the generic error with --json",
			status: http.StatusConflict,
			body:   "conflict",
			json:   true,
			want:   "error: request failed (status 409): conflict\n" + `{"error":{"status":409,"message":"request failed: conflict"}}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"POST /api/wiki/pages": reply(tc.status, tc.body),
			})
			f, out := testFactory(srv)
			args := []string{"create", "--product", "p1", "--title", "Guide", "--content", "# Hello", "--visibility", "public"}
			if tc.json {
				args = append(args, "--json")
			}
			wiki := NewWikiCmd(f)
			wiki.SetArgs(args)
			wiki.SetErr(&bytes.Buffer{})
			ran, err := wiki.ExecuteC()
			if err == nil {
				t.Fatal("the refused create must fail")
			}
			stderr := &bytes.Buffer{}
			WriteError(stderr, ran, args, err)
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q\nwant     %q", stderr.String(), tc.want)
			}
			if tc.json {
				assertJSONErrorIsTheLastLine(t, stderr.String())
			}
			if out.Len() != 0 {
				t.Errorf("a refused create must print nothing on stdout, got %q", out.String())
			}
			if calls := api.summary(); !slices.Equal(calls, []string{"POST /api/wiki/pages"}) {
				t.Errorf("a refused create must not retry or fall back to an update, got %v", calls)
			}
		})
	}
}

func TestWikiCreate_RequiresContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --content is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Guide"); err == nil {
		t.Fatal("wiki create without --content should error")
	}
}

func TestWikiUpdate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/wiki/pages/w1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "w1", "title": "NewTitle"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "update", "w1", "--title", "NewTitle", "--change-summary", "fix typo"); err != nil {
		t.Fatalf("wiki update error: %v", err)
	}
	if body["title"] != "NewTitle" {
		t.Errorf("update title: got %v want NewTitle", body["title"])
	}
	if body["changeSummary"] != "fix typo" {
		t.Errorf("update must send changeSummary wire key: got %v", body["changeSummary"])
	}
	if !strings.Contains(out.String(), "w1") {
		t.Errorf("update output should confirm page id: %s", out.String())
	}
}

func TestWikiUpdate_RequiresAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no update flags are passed")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "update", "w1"); err == nil {
		t.Fatal("wiki update with no flags should error")
	}
}
