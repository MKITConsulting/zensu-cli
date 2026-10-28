package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestKnowledgeContextPack(t *testing.T) {
	for _, tc := range []struct {
		name, ref, query string
		flags            []string
	}{
		{"key defaults", "ZEN-42", "", nil},
		{"uuid and filters", "61436a8d-12dd-4ff4-a30f-e1f230ea2576", "depth=3&include=build%2Cconstraints%2Cpassages", []string{"--depth", "3", "--include", "build, constraints, passages", "--json"}},
		{"escaped ref", "ZEN-42?include=lineage", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"feature_id": "feature", "max_depth": 2, "truncated": true, "layers": map[string]any{
				"build":     []any{map[string]any{"item_id": "doc", "uri": "file:README.md", "excerpt": "reference only", "depth": 1, "relation": "documented_by", "source_id": "source", "relevance": "direct", "reason": "direct_link", "path": []any{map[string]any{"item_id": "feature"}, map[string]any{"item_id": "doc", "relation": "documented_by", "direction": "outgoing"}}}},
				"placement": []any{}, "constraints": []any{}, "lineage": []any{},
			}, "related_layers": map[string]any{"build": []any{map[string]any{"item_id": "theme", "relevance": "related", "reason": "shared_document", "path": []any{map[string]any{"item_id": "feature"}, map[string]any{"item_id": "doc", "relation": "documented_by", "direction": "outgoing"}, map[string]any{"item_id": "theme", "relation": "documented_by", "direction": "incoming"}}}}}, "future_field": "preserved"}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/knowledge/features/"+tc.ref+"/context-pack" || r.URL.RawQuery != tc.query {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			args := append([]string{"context-pack", tc.ref}, tc.flags...)
			if err := runCmd(t, NewKnowledgeCmd(f), args...); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			wantRaw, _ := json.Marshal(payload)
			var want map[string]any
			_ = json.Unmarshal(wantRaw, &want)
			if !reflect.DeepEqual(got, want) || calls != 1 {
				t.Fatalf("response changed or unexpected calls: %s; calls=%d", out.String(), calls)
			}
		})
	}
}

func TestKnowledgeContextPackValidation(t *testing.T) {
	for _, args := range [][]string{
		{"context-pack"}, {"context-pack", " "}, {"context-pack", "ZEN-1", "extra"},
		{"context-pack", "ZEN-1", "--depth", "0"}, {"context-pack", "ZEN-1", "--depth", "6"},
		{"context-pack", "ZEN-1", "--include", ""}, {"context-pack", "ZEN-1", "--include", "build,unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input made a request") }))
			defer srv.Close()
			f, _ := testFactory(srv)
			if err := runCmd(t, NewKnowledgeCmd(f), args...); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestKnowledgeContextPackErrors(t *testing.T) {
	for _, status := range []int{401, 403, 404, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"context unavailable"}`)
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			err := runCmd(t, NewKnowledgeCmd(f), "context-pack", "ZEN-1", "--json")
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("status %d", status)) || out.Len() != 0 {
				t.Fatalf("status/error/output mismatch: %v %q", err, out.String())
			}
		})
	}
}

func TestKnowledgeSearchJSONPreservesPassages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"results":[{"itemId":"i1","text":"Evidence to reason from","uri":"file:README.md","expanded":true,"relation":"documented_by"}]}`)
	}))
	defer srv.Close()
	f, out := testFactory(srv)
	if err := runCmd(t, NewKnowledgeCmd(f), "search", "--query", "benchmarks", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Evidence to reason from", "file:README.md", "documented_by"} {
		if !strings.Contains(out.String(), value) {
			t.Errorf("missing %q", value)
		}
	}
}
