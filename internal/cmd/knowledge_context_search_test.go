package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextSearchCandidates(t *testing.T) {
	const product = "11111111-1111-4111-8111-111111111111"
	const feature = "22222222-2222-4222-8222-222222222222"
	item := func(i int) string { return fmt.Sprintf("%08d-3333-4333-8333-333333333333", i) }
	for _, filtered := range []bool{false, true} {
		t.Run(fmt.Sprint(filtered), func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/knowledge/search" {
					if r.Method != http.MethodPost {
						t.Fatal(r.Method)
					}
					var q map[string]any
					_ = json.NewDecoder(r.Body).Decode(&q)
					if q["query"] != "Fix login" || q["scope"] != "org" || q["limit"] != float64(10) {
						t.Errorf("query: %v", q)
					}
					hits := []map[string]any{{"itemId": "bad", "sourceType": "feature"}, {"itemId": item(9), "sourceType": "doc"}}
					for i := 1; i <= 8; i++ {
						hits = append(hits, map[string]any{"itemId": item(i), "sourceType": "feature", "title": "Login"})
						if i == 1 {
							hits = append(hits, hits[len(hits)-1])
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"results": hits})
					return
				}
				if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/knowledge/items/") {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				reads++
				_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/knowledge/items/"), "feature_id": feature, "product_id": product, "title": "Login", "content": strings.Repeat("é", 1300)})
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			args := []string{"context-search", "--query", " Fix login ", "--json"}
			if filtered {
				args = append(args, "--product", "99999999-9999-4999-8999-999999999999")
			}
			if err := runCmd(t, NewKnowledgeCmd(f), args...); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Candidates        []contextCandidate `json:"candidates"`
				SelectionRequired bool               `json:"selection_required"`
				Bounded           bool               `json:"bounded"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if reads != 5 || !result.SelectionRequired || !result.Bounded {
				t.Fatalf("reads=%d result=%+v", reads, result)
			}
			if filtered {
				if len(result.Candidates) != 0 {
					t.Fatal(result)
				}
			} else {
				if len(result.Candidates) != 1 {
					t.Fatal(result)
				}
				c := result.Candidates[0]
				if c.FeatureID != feature || c.ProductID != product || c.ItemID != item(1) || c.Title != "Login" || len([]rune(c.Excerpt)) != 1200 {
					t.Fatal(c)
				}
			}
			if strings.Contains(out.String(), "load_id") {
				t.Fatal("discovery must not invent a receipt")
			}
		})
	}
}

func TestContextSearchRejectsInvalidInputAndServerData(t *testing.T) {
	for _, args := range [][]string{{"context-search"}, {"context-search", "--query", " "}, {"context-search", "--query", strings.Repeat("x", 2001)}, {"context-search", "--query", "login", "--product", "bad"}, {"context-search", "--query", "login", "--product", ""}, {"context-search", "extra", "--query", "login"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached server") }))
		f, _ := testFactory(srv)
		if runCmd(t, NewKnowledgeCmd(f), args...) == nil {
			t.Errorf("accepted %v", args)
		}
		srv.Close()
	}
	for _, tc := range []struct {
		name, search, item string
		status             int
		wantError          bool
	}{
		{"empty", `{"results":[]}`, "", 200, false},
		{"bad JSON", `{`, "", 200, true},
		{"missing results", `{}`, "", 200, true},
		{"search failure", `{}`, "", 500, true},
		{"invalid identity", `{"results":[{"itemId":"33333333-3333-4333-8333-333333333333","sourceType":"feature"}]}`, `{"id":"33333333-3333-4333-8333-333333333333","feature_id":"bad"}`, 200, true},
		{"item JSON", `{"results":[{"itemId":"33333333-3333-4333-8333-333333333333","sourceType":"feature"}]}`, `{`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if r.URL.Path == "/api/knowledge/search" {
					fmt.Fprint(w, tc.search)
				} else {
					fmt.Fprint(w, tc.item)
				}
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			err := runCmd(t, NewKnowledgeCmd(f), "context-search", "--query", "login")
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v output=%s", err, out)
			}
			if tc.wantError && out.Len() != 0 {
				t.Fatal("partial discovery leaked on failure")
			}
		})
	}
}
