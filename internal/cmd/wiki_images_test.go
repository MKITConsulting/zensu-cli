package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWikiImagesPolicyAndUpload(t *testing.T) {
	const product = "1d051aea-4e32-40df-b1eb-bcf19eb31399"
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			data := []byte{0, 255, 1, 2, 34, 10}
			file := filepath.Join(t.TempDir(), "local.png")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.Method {
				case "GET":
					fmt.Fprint(w, `{"maxBytes":1024,"maxPixels":100,"maxDimension":16384}`)
				case "POST":
					raw, _ := io.ReadAll(r.Body)
					if !bytes.Equal(raw, data) || r.Header.Get("Content-Type") != "application/octet-stream" {
						t.Error("not unchanged binary upload")
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"asset","reference":"wiki-asset:asset","width":9,"height":6,"bytes":12,"mimeType":"image/png"}`)
				}
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			args := []string{"images", "upload", "--product", product, "--file", file}
			if jsonMode {
				args = append(args, "--json")
			}
			if err := runCmd(t, NewWikiCmd(f), args...); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "wiki-asset:asset") {
				t.Fatal(out.String())
			}
			if len(calls) != 2 || calls[0] != "GET /api/wiki/images/"+product+"/policy" || calls[1] != "POST /api/wiki/images/"+product {
				t.Fatalf("calls %v", calls)
			}
			out.Reset()
			if err := runCmd(t, NewWikiCmd(f), "images", "policy", "--product", product); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"maxDimension": 16384`) {
				t.Fatal(out.String())
			}
		})
	}
}
func TestWikiImagesRejectInvalidFilesAndServerFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "image.png")
	if err := os.WriteFile(file, []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, file, product, policy string
		status                      int
		upload                      bool
	}{
		{name: "missing product", file: file},
		{name: "missing file", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399"},
		{name: "remote URL", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: "https://example.com/image.png"},
		{name: "empty", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: empty},
		{name: "directory", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: dir},
		{name: "missing", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: filepath.Join(dir, "missing")},
		{name: "policy limit", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: file, policy: `{"maxBytes":2}`, status: 200},
		{name: "policy invalid", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: file, policy: `{}`, status: 200},
		{name: "policy denied", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: file, status: 403},
		{name: "upload rejected", product: "1d051aea-4e32-40df-b1eb-bcf19eb31399", file: file, policy: `{"maxBytes":1024}`, status: 400, upload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					uploads++
					w.WriteHeader(tc.status)
					fmt.Fprint(w, `{"message":"image rejected"}`)
					return
				}
				if tc.upload {
					fmt.Fprint(w, tc.policy)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.policy)
			}))
			defer srv.Close()
			f, _ := testFactory(srv)
			err := runCmd(t, NewWikiCmd(f), "images", "upload", "--product", tc.product, "--file", tc.file)
			if err == nil {
				t.Fatal("expected error")
			}
			if uploads > 0 && !tc.upload {
				t.Fatal("invalid file uploaded")
			}
		})
	}
}
func TestWikiImageHelp(t *testing.T) {
	for _, args := range [][]string{{"create", "--help"}, {"update", "--help"}, {"images", "--help"}} {
		f, out := testFactory(nil)
		cmd := NewWikiCmd(f)
		cmd.SetOut(out)
		if err := runCmd(t, cmd, args...); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"wiki-asset:", "{width=50%}", "1–4096px", "never publishes"} {
			if !strings.Contains(out.String(), s) {
				t.Errorf("%v help missing %s", args, s)
			}
		}
	}
}
