package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMocksList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "m1", "feature_id": "f1", "mock_type": "html", "title": "Login screen", "file_name": "login.html", "mime_type": "text/html", "file_size_bytes": 128},
				{"id": "m2", "feature_id": "f1", "mock_type": "image", "title": nil, "file_name": "hero.png", "mime_type": "image/png", "file_size_bytes": 4096},
			},
			"total":  2,
			"limit":  50,
			"offset": 0,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1"); err != nil {
		t.Fatalf("mocks list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"TYPE", "TITLE", "FILE_NAME", "ID", "html", "Login screen", "login.html", "m1", "image", "hero.png", "m2"} {
		if !strings.Contains(got, want) {
			t.Errorf("mocks list table missing %q in:\n%s", want, got)
		}
	}
}

func TestMocksList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"id": "m1", "mock_type": "html", "file_name": "login.html"}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1", "--json"); err != nil {
		t.Fatalf("mocks list --json error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"file_name": "login.html"`) {
		t.Errorf("mocks list --json should pass through raw JSON, got:\n%s", got)
	}
	if strings.Contains(got, "FILE_NAME") {
		t.Errorf("mocks list --json should not render the table header, got:\n%s", got)
	}
}

func TestMocksList_RequiresFeatureArg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without a feature id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("mocks list without a feature id should error")
	}
}

func TestMocksGet_Metadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("metadata path should hit the list endpoint, got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "m1", "feature_id": "f1", "mock_type": "image", "title": "Hero", "file_name": "hero.png", "mime_type": "image/png", "file_size_bytes": 4096},
				{"id": "m2", "feature_id": "f1", "mock_type": "html", "title": "Login", "file_name": "login.html", "mime_type": "text/html", "file_size_bytes": 128},
			},
			"total": 2,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m2"); err != nil {
		t.Fatalf("mocks get error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"m2", "html", "Login", "login.html", "text/html", "128 bytes"} {
		if !strings.Contains(got, want) {
			t.Errorf("mocks get metadata missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hero.png") {
		t.Errorf("mocks get should only print the requested mock, leaked sibling in:\n%s", got)
	}
}

func TestMocksGet_Raw(t *testing.T) {
	const htmlBody = "<!doctype html><html><body><h1>Login</h1></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks/m2/raw" {
			t.Errorf("--raw should hit the raw endpoint, got %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m2", "--raw"); err != nil {
		t.Fatalf("mocks get --raw error: %v", err)
	}
	if got := out.String(); got != htmlBody {
		t.Errorf("mocks get --raw should write the body verbatim:\n got: %q\nwant: %q", got, htmlBody)
	}
}

func TestMocksGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"id": "m1", "mock_type": "html", "file_name": "login.html"}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "does-not-exist"); err == nil {
		t.Fatal("mocks get for an unknown mock id should error")
	}
}

func TestMocksGet_RequiresBothArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without both ids")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1"); err == nil {
		t.Fatal("mocks get without a mock id should error")
	}
}

func TestMocksList_RequestError(t *testing.T) {
	f, _ := failingFactory()
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1"); !errors.Is(err, errTransport) {
		t.Fatalf("mocks list should surface the transport error, got: %v", err)
	}
}

func TestMocksList_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1"); err == nil {
		t.Fatal("mocks list should error on an undecodable body")
	}
	if got := out.String(); got != "" {
		t.Errorf("mocks list must not render a table from an undecodable body, got:\n%s", got)
	}
}

func TestMocksList_JSONPassesThroughUndecodableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1", "--json"); err != nil {
		t.Fatalf("mocks list --json should pass an undecodable body through, got error: %v", err)
	}
	if got := out.String(); got != "not json\n" {
		t.Errorf("mocks list --json body: got %q want %q", got, "not json\n")
	}
}

func TestMocksGet_RawRequestError(t *testing.T) {
	f, _ := failingFactory()
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m1", "--raw"); !errors.Is(err, errTransport) {
		t.Fatalf("mocks get --raw should surface the transport error, got: %v", err)
	}
}

func TestMocksGet_MetadataRequestError(t *testing.T) {
	f, _ := failingFactory()
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m1"); !errors.Is(err, errTransport) {
		t.Fatalf("mocks get should surface the transport error, got: %v", err)
	}
}

func TestMocksGet_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m1"); err == nil {
		t.Fatal("mocks get should error on an undecodable body")
	}
	if got := out.String(); got != "" {
		t.Errorf("mocks get must not render metadata from an undecodable body, got:\n%s", got)
	}
}

func writeMockFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
	return path
}

func TestMocksCreate_UploadsMultipart(t *testing.T) {
	const htmlBody = "<!doctype html><html><body><h1>Login</h1></body></html>"
	var gotMethod, gotPath, gotFileName, gotFileBody, gotTitle, gotAltText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("body must be multipart/form-data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("multipart field %q missing: %v", "file", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		gotFileName, gotFileBody = header.Filename, string(raw)
		gotTitle, gotAltText = r.FormValue("title"), r.FormValue("altText")

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m9", "feature_id": "f1", "mock_type": "html", "title": "Login screen",
			"file_name": "login.html", "mime_type": "text/html", "file_size_bytes": len(htmlBody),
		})
	}))
	defer srv.Close()

	path := writeMockFile(t, "login.html", htmlBody)
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path, "--title", "Login screen", "--alt-text", "The login form"); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/api/features/f1/mocks" {
		t.Errorf("request: got %s %s want POST /api/features/f1/mocks", gotMethod, gotPath)
	}
	if gotFileName != "login.html" {
		t.Errorf("upload should keep the file's base name: got %q want %q", gotFileName, "login.html")
	}
	if gotFileBody != htmlBody {
		t.Errorf("uploaded bytes: got %q want %q", gotFileBody, htmlBody)
	}
	if gotTitle != "Login screen" {
		t.Errorf("title field: got %q want %q", gotTitle, "Login screen")
	}
	if gotAltText != "The login form" {
		t.Errorf("altText field: got %q want %q", gotAltText, "The login form")
	}

	got := out.String()
	if !strings.Contains(got, "Uploaded html mock to feature f1") {
		t.Errorf("the summary line must name the mock type and the feature id, got:\n%s", got)
	}
	for _, want := range []string{"m9", "Login screen", "login.html", "text/html", fmt.Sprintf("%d bytes", len(htmlBody))} {
		if !strings.Contains(got, want) {
			t.Errorf("mocks create output missing %q in:\n%s", want, got)
		}
	}
}

func TestMocksCreate_OmitsUnsetOptionalFields(t *testing.T) {
	var titlePresent, altPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("body must be multipart/form-data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, titlePresent = r.MultipartForm.Value["title"]
		_, altPresent = r.MultipartForm.Value["altText"]
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m9", "mock_type": "image", "title": nil, "file_name": "hero.png",
			"mime_type": "image/png", "file_size_bytes": 3,
		})
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}
	if titlePresent {
		t.Error("title field must be omitted when --title is not given")
	}
	if altPresent {
		t.Error("altText field must be omitted when --alt-text is not given")
	}
	if got := out.String(); !strings.Contains(got, "hero.png") || !strings.Contains(got, "m9") {
		t.Errorf("mocks create output should report the created mock, got:\n%s", got)
	}
}

func TestMocksCreate_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("body must be multipart/form-data: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "m9", "file_name": "hero.png"})
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path, "--json"); err != nil {
		t.Fatalf("mocks create --json error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"file_name": "hero.png"`) {
		t.Errorf("mocks create --json should pass through raw JSON, got:\n%s", got)
	}
	if strings.Contains(got, "Content type:") {
		t.Errorf("mocks create --json should not render the summary, got:\n%s", got)
	}
}

func TestMocksCreate_MissingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when the local file cannot be read")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", filepath.Join(t.TempDir(), "absent.png"))
	if err == nil {
		t.Fatal("mocks create with an unreadable file should error")
	}
	if !strings.Contains(err.Error(), "reading mock file") {
		t.Errorf("error should name the local read failure, got: %v", err)
	}
}

func TestMocksCreate_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "unsupported_media_type", "message": "unsupported file type (allowed: png, jpg, jpeg, html)",
		})
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", path)
	if err == nil {
		t.Fatal("mocks create should surface a non-2xx API response as an error")
	}
	if !strings.Contains(err.Error(), "unsupported file type") || !strings.Contains(err.Error(), "415") {
		t.Errorf("error should carry the API message and status, got: %v", err)
	}
}

func TestMocksCreate_MalformedSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path); err == nil {
		t.Fatal("mocks create should error when a 201 carries an undecodable body")
	}
	if got := out.String(); got != "" {
		t.Errorf("mocks create must not print a summary from an undecodable body, got:\n%s", got)
	}
}

func TestMocksCreate_RequiresBothArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without both a feature id and a file")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1"); err == nil {
		t.Fatal("mocks create without a file argument should error")
	}
}

func TestMocksCreate_RejectsUnsupportedExtensionLocally(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an unsupported file must never be read or sent")
	}))
	defer srv.Close()

	path := writeMockFile(t, "secrets.txt", "SUPER_SECRET=1")
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", path)
	if err == nil {
		t.Fatal("mocks create must refuse an unsupported extension before uploading")
	}
	if !strings.Contains(err.Error(), ".txt") {
		t.Errorf("the error should name the rejected extension, got: %v", err)
	}
	if strings.Contains(err.Error(), "SUPER_SECRET") || strings.Contains(out.String(), "SUPER_SECRET") {
		t.Error("the file contents must never appear in output")
	}
}

func TestMocksCreate_UploadsBinaryBytesUnchanged(t *testing.T) {
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0xfe, 0x01}
	var gotFileBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("body must be multipart/form-data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("multipart field \"file\" missing: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		gotFileBody, _ = io.ReadAll(file)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m9", "mock_type": "image", "file_name": "hero.png",
			"mime_type": "image/png", "file_size_bytes": len(pngBytes),
		})
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "hero.png")
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatalf("writing binary fixture: %v", err)
	}
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}
	if !bytes.Equal(gotFileBody, pngBytes) {
		t.Errorf("binary payload was altered in transit:\n got: %v\nwant: %v", gotFileBody, pngBytes)
	}
}

func TestMocksCreate_EscapesAwkwardFileName(t *testing.T) {
	const awkward = "a\"quote\nand-newline.png"
	var gotFileName string
	var partHeaderCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("body must be multipart/form-data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Errorf("first part unreadable: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotFileName = part.FileName()
		partHeaderCount = len(part.Header)
		_ = part.Close()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "m9", "mock_type": "image", "file_name": "sanitized.png"})
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), awkward)
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("windows filesystems reject this name: %v", err)
		}
		t.Fatalf("writing awkward fixture: %v", err)
	}
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}
	if strings.ContainsAny(gotFileName, "\r\n") {
		t.Errorf("the CLI must strip CR/LF from the transmitted file name, got %q", gotFileName)
	}
	if !strings.HasSuffix(gotFileName, ".png") {
		t.Errorf("the extension decides the mock type server-side and must survive, got %q", gotFileName)
	}
	if partHeaderCount != 2 {
		t.Errorf("the file part must carry exactly Content-Disposition and Content-Type, got %d headers", partHeaderCount)
	}
	if !strings.Contains(gotFileName, "quote") {
		t.Errorf("the file name should still round-trip recognizably, got %q", gotFileName)
	}
}

func TestMockTypeForFile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wantType string
		wantOK   bool
	}{
		{"hero.png", "image", true},
		{"hero.PNG", "image", true},
		{"shot.jpg", "image", true},
		{"shot.jpeg", "image", true},
		{"login.html", "html", true},
		{"login.htm", "html", true},
		{"login.HTML", "html", true},
		{"notes.txt", "", false},
		{"archive.tar.gz", "", false},
		{"noextension", "", false},
	} {
		gotType, gotOK := mockTypeForFile(tc.name)
		if gotOK != tc.wantOK || gotType != tc.wantType {
			t.Errorf("mockTypeForFile(%q) = (%q, %v), want (%q, %v)", tc.name, gotType, gotOK, tc.wantType, tc.wantOK)
		}
	}
}

func TestMocksCreate_HelpNamesEveryAcceptedExtension(t *testing.T) {
	f := &Factory{Out: &bytes.Buffer{}}
	create := findMocksSub(t, NewMocksCmd(f), "create")
	for _, want := range []string{".png -> image", ".jpg -> image", ".jpeg -> image", ".html -> html", ".htm -> html"} {
		if !strings.Contains(create.Long, want) {
			t.Errorf("create help must name %q, got:\n%s", want, create.Long)
		}
	}
}

func findMocksSub(t *testing.T, group *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range group.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("mocks group has no %q subcommand", name)
	return nil
}

func TestMocksCreate_EscapesFeatureIDInPath(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "m9", "mock_type": "image", "file_name": "hero.png"})
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f/1 x", path); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}
	if gotEscapedPath != "/api/features/f%2F1%20x/mocks" {
		t.Errorf("the feature id must be path-escaped: got %q", gotEscapedPath)
	}
}

func TestMocksCreate_SanitizesServerStringsInOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m9", "mock_type": "image", "title": "Login\u001b[2Jscreen",
			"file_name": "hero\u202egnp.png", "mime_type": "image/png", "file_size_bytes": 3,
		})
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "png")
	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "create", "f1", path); err != nil {
		t.Fatalf("mocks create error: %v", err)
	}
	got := out.String()
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x202e) {
		t.Errorf("server strings must be sanitized before printing, got %q", got)
	}
	if !strings.Contains(got, "Login[2Jscreen") {
		t.Errorf("the printable part of the title must survive, got %q", got)
	}
	if !strings.Contains(got, "herognp.png") {
		t.Errorf("the file name must survive with only the bidi override removed, got %q", got)
	}
}

func TestMocksList_SanitizesServerStringsInTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id": "m\u001b[2J1", "mock_type": "ht\u001bml", "title": "Log\u202ein", "file_name": "log\u001bin.html",
			}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1"); err != nil {
		t.Fatalf("mocks list error: %v", err)
	}
	got := out.String()
	for _, bad := range []rune{0x1b, 0x202e} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("the table must not emit %#U, got %q", bad, got)
		}
	}
	for _, want := range []string{"[2J1", "html", "Login", "login.html"} {
		if !strings.Contains(got, want) {
			t.Errorf("every sanitized field must still render its printable residue %q, got %q", want, got)
		}
	}
}

func TestMocksCreate_RefusesNonRegularFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a non-regular file must never be uploaded")
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "shot.png")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating directory fixture: %v", err)
	}
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", dir)
	if err == nil {
		t.Fatal("mocks create must refuse a directory even when its name ends in an accepted extension")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("the error should say the path is not a regular file, got: %v", err)
	}
}

func TestMocksCreate_RefusesOversizedFile(t *testing.T) {
	t.Setenv(maxUploadBytesEnv, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an oversized file must never be uploaded")
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "huge.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating sparse fixture: %v", err)
	}
	if err := file.Truncate(maxMockBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("sizing sparse fixture: %v", err)
	}
	_ = file.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	err = runCmd(t, cmd, "create", "f1", path)
	if err == nil {
		t.Fatal("mocks create must refuse a file above the local size limit")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("the error should name the size limit, got: %v", err)
	}
}

func TestMocksGet_SanitizesServerStringsInMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id": "m\u001b[2J2", "mock_type": "ht\u001bml", "title": "L\u202eogin",
				"file_name": "log\u001bin.html", "mime_type": "text/\u001bhtml", "file_size_bytes": 128,
			}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m\u001b[2J2"); err != nil {
		t.Fatalf("mocks get error: %v", err)
	}
	got := out.String()
	for _, bad := range []rune{0x1b, 0x202e} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("mocks get metadata must not emit %#U, got %q", bad, got)
		}
	}
	for _, want := range []string{"m[2J2", "html", "Login", "login.html", "text/html", "128 bytes"} {
		if !strings.Contains(got, want) {
			t.Errorf("every sanitized field must still render %q, got %q", want, got)
		}
	}
}

func TestMocksGet_EscapesMockIDInRawPath(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "../../admin x", "--raw"); err != nil {
		t.Fatalf("mocks get --raw error: %v", err)
	}
	if gotEscapedPath != "/api/features/f1/mocks/..%2F..%2Fadmin%20x/raw" {
		t.Errorf("the mock id must be path-escaped: got %q", gotEscapedPath)
	}
}

func TestMocksCreate_RefusesSymlink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a symlink must never be uploaded")
	}))
	defer srv.Close()

	dir := t.TempDir()
	secret := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatalf("writing secret fixture: %v", err)
	}
	link := filepath.Join(dir, "mock.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("this platform cannot create symlinks: %v", err)
	}

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", link)
	if err == nil {
		t.Fatal("mocks create must refuse a symlink whose name ends in an accepted extension")
	}
	if !strings.Contains(err.Error(), "pass the file it points at") {
		t.Errorf("the error should say the path is a symlink, got: %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(out.String(), "PRIVATE KEY") {
		t.Error("the linked file's contents must never appear in output")
	}
}

type growingReader struct{ left int }

func (g *growingReader) Read(p []byte) (int, error) {
	if g.left <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > g.left {
		n = g.left
	}
	for i := range p[:n] {
		p[i] = 'x'
	}
	g.left -= n
	return n, nil
}

func TestBuildMockBody_RefusesAReaderThatOutgrowsTheLimit(t *testing.T) {
	_, _, err := buildMockBody("hero.png", &growingReader{left: 64}, 32, "", "")
	if err == nil {
		t.Fatal("buildMockBody must refuse a source that yields more than the limit")
	}
	if !strings.Contains(err.Error(), "grew past") {
		t.Errorf("the error should name the grow-while-reading case, got: %v", err)
	}
}

func TestMaxMockUploadBytes_HonorsTheEnvOverride(t *testing.T) {
	t.Setenv(maxUploadBytesEnv, "")
	if got := maxMockUploadBytes(); got != maxMockBytes {
		t.Errorf("without the env var the built-in limit applies: got %d want %d", got, maxMockBytes)
	}
	t.Setenv(maxUploadBytesEnv, "99")
	if got := maxMockUploadBytes(); got != 99 {
		t.Errorf("the env override must win: got %d want 99", got)
	}
	t.Setenv(maxUploadBytesEnv, "not-a-number")
	if got := maxMockUploadBytes(); got != maxMockBytes {
		t.Errorf("an unparseable override must fall back to the built-in limit: got %d", got)
	}
	t.Setenv(maxUploadBytesEnv, "9223372036854775807")
	if got := maxMockUploadBytes(); got != maxUploadBytesCeiling {
		t.Errorf("an override that would overflow limit+1 must be clamped: got %d want %d", got, maxUploadBytesCeiling)
	}
}

func TestMocksCreate_HonorsTheEnvSizeOverride(t *testing.T) {
	t.Setenv(maxUploadBytesEnv, "10")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a file above the overridden limit must never be uploaded")
	}))
	defer srv.Close()

	path := writeMockFile(t, "hero.png", "eleven bytes")
	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	err := runCmd(t, cmd, "create", "f1", path)
	if err == nil {
		t.Fatal("the env override must bound the upload, not just the helper")
	}
	if !strings.Contains(err.Error(), "exceeds the 10 byte limit") {
		t.Errorf("the refusal should name the overridden limit, got: %v", err)
	}
}
