package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductsList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "p1", "name": "Alpha", "slug": "alpha", "product_type": "public"},
				{"id": "p2", "name": "Beta", "slug": "beta", "product_type": nil},
			},
			"total": 2, "page": 1, "perPage": 20,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "list"); err != nil {
		t.Fatalf("products list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "NAME", "TYPE", "p1", "Alpha", "public", "p2", "Beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestProductsList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "p1", "name": "Alpha"}}, "total": 1})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "list", "--json"); err != nil {
		t.Fatalf("products list --json error: %v", err)
	}
	if !strings.Contains(out.String(), "\"data\"") {
		t.Errorf("--json should emit raw envelope, got:\n%s", out.String())
	}
}

func TestProductsGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/products/p1" {
			t.Errorf("path: got %s want /api/products/p1", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1", "name": "Alpha", "slug": "alpha"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "get", "p1"); err != nil {
		t.Fatalf("products get error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "p1") || !strings.Contains(got, "Alpha") {
		t.Errorf("get output missing fields: %s", got)
	}
}

func TestProductsCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p9", "name": "Gamma", "product_type": "public_product"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "create", "--name", "Gamma", "--type", "public"); err != nil {
		t.Fatalf("products create error: %v", err)
	}
	if body["name"] != "Gamma" {
		t.Errorf("body name: got %v want Gamma", body["name"])
	}
	if body["productType"] != "public_product" {
		t.Errorf("short --type public must map to backend value: got %v want public_product", body["productType"])
	}
	got := out.String()
	if !strings.Contains(got, "p9") || !strings.Contains(got, "Gamma") {
		t.Errorf("create output missing created product: %s", got)
	}
}

func TestProductsCreate_TypeAliases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"public", "public_product"},
		{"internal", "internal_product"},
		{"hybrid", "hybrid"},
		{"public_product", "public_product"},
		{"internal_product", "internal_product"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			var got any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				got = body["productType"]
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1"})
			}))
			defer srv.Close()

			f, _ := testFactory(srv)
			cmd := NewProductsCmd(f)
			if err := runCmd(t, cmd, "create", "--name", "X", "--type", tc.in); err != nil {
				t.Fatalf("create error: %v", err)
			}
			if got != tc.want {
				t.Errorf("--type %q: got productType %v want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestProductsCreate_TypeNormalizesCaseAndSpace(t *testing.T) {
	var got any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body["productType"]
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "create", "--name", "X", "--type", "  Public  "); err != nil {
		t.Fatalf("create error: %v", err)
	}
	if got != "public_product" {
		t.Errorf("--type with caps/whitespace must normalize to the backend value: got %v want public_product", got)
	}
}

func TestProductsCreate_RequiresName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when --name missing")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "create", "--type", "public"); err == nil {
		t.Fatal("create without --name should error")
	}
}

func TestProductsVisionCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/visions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "v9", "title": "MVP"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "vision-create", "--product", "p1", "--title", "MVP", "--content", "Build a CLI", "--source", "claude-code"); err != nil {
		t.Fatalf("vision-create error: %v", err)
	}
	if body["title"] != "MVP" || body["content"] != "Build a CLI" || body["productId"] != "p1" || body["source"] != "claude-code" {
		t.Errorf("vision-create body must carry title, content, productId, source: %v", body)
	}
	if !strings.Contains(out.String(), "v9") || !strings.Contains(out.String(), "MVP") {
		t.Errorf("vision-create output missing created vision: %s", out.String())
	}
}

func TestProductsVisionCreate_OmitsProductWhenUnset(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "v9"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "vision-create", "--title", "Greenfield", "--content", "idea"); err != nil {
		t.Fatalf("vision-create error: %v", err)
	}
	if _, ok := body["productId"]; ok {
		t.Errorf("productId must be omitted for greenfield visions, got: %v", body)
	}
}

func TestProductsVisionCreate_RequiresTitleAndContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title or --content is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "vision-create", "--title", "Only Title"); err == nil {
		t.Fatal("vision-create without --content should error")
	}
	cmd2 := NewProductsCmd(f)
	if err := runCmd(t, cmd2, "vision-create", "--content", "Only Content"); err == nil {
		t.Fatal("vision-create without --title should error")
	}
}

func TestProductsVisionGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/visions/v1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "v1", "title": "MVP", "content": "stuff"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "vision-get", "v1"); err != nil {
		t.Fatalf("vision-get error: %v", err)
	}
	if !strings.Contains(out.String(), "v1") || !strings.Contains(out.String(), "MVP") {
		t.Errorf("vision-get output missing fields: %s", out.String())
	}
}

func TestProductsBootstrapApply(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/visions/v1/bootstrap/apply" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"componentsCreated": 2, "featuresCreated": 5, "subfeaturesCreated": 1})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	result := `{"components":[{"name":"Auth","slug":"auth"}],"features":[{"title":"Login","slug":"login","component":"auth"}]}`
	if err := runCmd(t, cmd, "bootstrap-apply", "v1", "--result", result); err != nil {
		t.Fatalf("bootstrap-apply error: %v", err)
	}
	comps, _ := body["components"].([]any)
	feats, _ := body["features"].([]any)
	if len(comps) != 1 || len(feats) != 1 {
		t.Errorf("bootstrap-apply body must forward components and features: %v", body)
	}
	if !strings.Contains(out.String(), "2 components") || !strings.Contains(out.String(), "5 features") {
		t.Errorf("bootstrap-apply confirmation missing counts: %s", out.String())
	}
}

func TestProductsBootstrapApply_RequiresResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --result is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "bootstrap-apply", "v1"); err == nil {
		t.Fatal("bootstrap-apply without --result should error")
	}
}

func TestProductsBootstrapApply_RejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --result is not valid JSON")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "bootstrap-apply", "v1", "--result", "not json"); err == nil {
		t.Fatal("bootstrap-apply with invalid --result JSON should error before any request")
	}
}

func TestProductsBootstrapStep(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/visions/v1/bootstrap/step" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "v1", "post_bootstrap_step": 3})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "bootstrap-step", "v1", "3"); err != nil {
		t.Fatalf("bootstrap-step error: %v", err)
	}
	if got, _ := body["step"].(float64); got != 3 {
		t.Errorf("bootstrap-step body must send step=3: got %v", body["step"])
	}
	if !strings.Contains(out.String(), "3") {
		t.Errorf("bootstrap-step confirmation missing step: %s", out.String())
	}
}

func TestProductsBootstrapStep_RejectsNonNumericStep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when step is not a number")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "bootstrap-step", "v1", "abc"); err == nil {
		t.Fatal("bootstrap-step with a non-numeric step should error before any request")
	}
}

func TestProductsImport(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/repo-import" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ri9", "repo_url": "https://github.com/acme/repo"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "import", "p1", "--repo-url", "https://github.com/acme/repo", "--repo-type", "github"); err != nil {
		t.Fatalf("import error: %v", err)
	}
	if body["repoUrl"] != "https://github.com/acme/repo" || body["repoType"] != "github" {
		t.Errorf("import body must carry repoUrl and repoType: %v", body)
	}
	if !strings.Contains(out.String(), "ri9") {
		t.Errorf("import output missing created import id: %s", out.String())
	}
}

func TestProductsImport_RequiresRepoURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --repo-url is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "import", "p1"); err == nil {
		t.Fatal("import without --repo-url should error")
	}
}
