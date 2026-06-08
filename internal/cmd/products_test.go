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
