package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type featureMock struct {
	ID            string  `json:"id"`
	FeatureID     string  `json:"feature_id"`
	MockType      string  `json:"mock_type"`
	Title         *string `json:"title"`
	FileName      string  `json:"file_name"`
	MimeType      string  `json:"mime_type"`
	FileSizeBytes int32   `json:"file_size_bytes"`
}

func NewMocksCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mocks",
		Aliases: []string{"mock"},
		Short:   "Upload and inspect a feature's design mocks",
		Long: "Upload and inspect a feature's design mocks (HTML markup or image previews). Use 'create' to " +
			"upload a mock from a local file, 'list' to enumerate a feature's mocks and 'get' to pull a single " +
			"mock's metadata or raw content. Pulling an HTML mock's raw markup is the way to load a feature's " +
			"mock into your working context.",
	}
	cmd.AddCommand(
		newMocksCreateCmd(f),
		newMocksListCmd(f),
		newMocksGetCmd(f),
	)
	return cmd
}

const maxMockBytes = 32 << 20

const maxUploadBytesEnv = "ZENSU_MAX_UPLOAD_BYTES"

const maxUploadBytesCeiling = 512 << 20

func maxMockUploadBytes() int64 {
	if raw := os.Getenv(maxUploadBytesEnv); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			if n > maxUploadBytesCeiling {
				return maxUploadBytesCeiling
			}
			return n
		}
	}
	return maxMockBytes
}

var mockExtensions = []struct{ ext, mockType string }{
	{".png", "image"},
	{".jpg", "image"},
	{".jpeg", "image"},
	{".html", "html"},
	{".htm", "html"},
}

func mockExtensionHelp() string {
	parts := make([]string, 0, len(mockExtensions))
	for _, e := range mockExtensions {
		parts = append(parts, e.ext+" -> "+e.mockType)
	}
	return strings.Join(parts, ", ")
}

func mockTypeForFile(name string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range mockExtensions {
		if e.ext == ext {
			return e.mockType, true
		}
	}
	return "", false
}

func newMocksCreateCmd(f *Factory) *cobra.Command {
	var title, altText string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create <feature-id> <file>",
		Short: "Upload a design mock to a feature",
		Long: "Upload a design mock from a local file to a feature. The file extension decides the mock " +
			"type (" + mockExtensionHelp() + "); HTML mocks have their markup stored as searchable text. " +
			"Any other extension is refused locally, before the file is read or sent. Use --title and " +
			"--alt-text to describe the mock.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, path := args[0], args[1]
			fileName := filepath.Base(path)
			if _, ok := mockTypeForFile(fileName); !ok {
				return fmt.Errorf("unsupported mock file type %q (accepted: %s)", filepath.Ext(fileName), mockExtensionHelp())
			}
			body, contentType, err := mockUploadBody(fileName, path, title, altText)
			if err != nil {
				return err
			}
			raw, err := f.requestWithContentType(cmd.Context(), http.MethodPost, mockCollectionPath(featureID), contentType, body)
			if err != nil {
				return err
			}
			if asJSON {
				if err := printJSON(f.Out, raw); err != nil {
					return uploadAcceptedError(featureID, err)
				}
				return nil
			}
			var m featureMock
			if err := json.Unmarshal(raw, &m); err != nil {
				return uploadAcceptedError(featureID, err)
			}
			fmt.Fprintf(f.Out, "Uploaded %s mock to feature %s\n", sanitizeTerminal(m.MockType), sanitizeTerminal(featureID))
			fmt.Fprintf(f.Out, "ID:           %s\n", sanitizeTerminal(m.ID))
			fmt.Fprintf(f.Out, "Title:        %s\n", sanitizeTerminal(derefMockStr(m.Title)))
			fmt.Fprintf(f.Out, "File name:    %s\n", sanitizeTerminal(m.FileName))
			fmt.Fprintf(f.Out, "Content type: %s\n", sanitizeTerminal(m.MimeType))
			fmt.Fprintf(f.Out, "Size:         %d bytes\n", m.FileSizeBytes)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "title shown for the mock")
	cmd.Flags().StringVar(&altText, "alt-text", "", "alternative text describing the mock")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func mockUploadBody(fileName, path, title, altText string) ([]byte, string, error) {
	limit := maxMockUploadBytes()

	link, err := os.Lstat(path)
	if err != nil {
		return nil, "", fmt.Errorf("inspecting mock file: %w", err)
	}
	if link.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("%s is a symlink; pass the file it points at so the upload is what you expect", sanitizeTerminal(path))
	}
	if !link.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s is not a regular file", sanitizeTerminal(path))
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("opening mock file: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("inspecting opened mock file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s stopped being a regular file while it was being opened", sanitizeTerminal(path))
	}
	if err := verifyPathUnchanged(path, link, info); err != nil {
		return nil, "", err
	}
	if info.Size() > limit {
		return nil, "", fmt.Errorf("mock file is %d bytes, which exceeds the %d byte limit", info.Size(), limit)
	}
	return buildMockBody(fileName, file, limit, title, altText)
}

func uploadAcceptedError(featureID string, cause error) error {
	return fmt.Errorf("the server accepted the upload but its response could not be shown: %w; run zensu mocks list %s before retrying so you do not create a duplicate", cause, sanitizeTerminal(featureID))
}

func verifyPathUnchanged(path string, before, opened os.FileInfo) error {
	again, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("re-checking mock file: %w", err)
	}
	if !os.SameFile(again, opened) || !os.SameFile(before, opened) {
		return fmt.Errorf("%s changed while it was being opened; not uploading it", sanitizeTerminal(path))
	}
	return nil
}

func buildMockBody(fileName string, r io.Reader, limit int64, title, altText string) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", sanitizeUploadFileName(fileName))
	if err != nil {
		return nil, "", err
	}
	written, err := io.Copy(part, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading mock file: %w", err)
	}
	if written > limit {
		return nil, "", fmt.Errorf("mock file grew past the %d byte limit while being read", limit)
	}
	for _, field := range []struct{ name, value string }{{"title", title}, {"altText", altText}} {
		if field.value == "" {
			continue
		}
		if err := mw.WriteField(field.name, field.value); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func newMocksListCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list <feature-id>",
		Short:        "List a feature's design mocks",
		Long:         "List a feature's design mocks. Returns each mock's type (image|html), title, file name and id.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.listAll(cmd.Context(), mockCollectionPath(args[0]), nil, "mocks")
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []featureMock `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "TYPE\tTITLE\tFILE_NAME\tID")
			for _, m := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sanitizeTerminal(m.MockType), sanitizeTerminal(derefMockStr(m.Title)), sanitizeTerminal(m.FileName), sanitizeTerminal(m.ID))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, listJSONUsage)
	return cmd
}

func newMocksGetCmd(f *Factory) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "get <feature-id> <mock-id>",
		Short: "Get a mock's metadata or raw content",
		Long: "Get a single mock. By default prints the mock's metadata (type, title, file name, content type, " +
			"size). With --raw, prints the mock's raw content to stdout instead — for HTML mocks this is the " +
			"assembled markup, which lets you load the mock into your working context.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, mockID := args[0], args[1]
			if raw {
				body, err := f.request(cmd.Context(), http.MethodGet, mockCollectionPath(featureID)+"/"+url.PathEscape(mockID)+"/raw", nil)
				if err != nil {
					return err
				}
				_, err = f.Out.Write(body)
				return err
			}

			listRaw, err := f.listAll(cmd.Context(), mockCollectionPath(featureID), nil, "mocks")
			if err != nil {
				return err
			}
			var env struct {
				Data []featureMock `json:"data"`
			}
			if err := json.Unmarshal(listRaw, &env); err != nil {
				return err
			}
			for _, m := range env.Data {
				if m.ID != mockID {
					continue
				}
				fmt.Fprintf(f.Out, "ID:           %s\n", sanitizeTerminal(m.ID))
				fmt.Fprintf(f.Out, "Type:         %s\n", sanitizeTerminal(m.MockType))
				fmt.Fprintf(f.Out, "Title:        %s\n", sanitizeTerminal(derefMockStr(m.Title)))
				fmt.Fprintf(f.Out, "File name:    %s\n", sanitizeTerminal(m.FileName))
				fmt.Fprintf(f.Out, "Content type: %s\n", sanitizeTerminal(m.MimeType))
				fmt.Fprintf(f.Out, "Size:         %d bytes\n", m.FileSizeBytes)
				return nil
			}
			return fmt.Errorf("mock %s not found for feature %s", sanitizeTerminal(mockID), sanitizeTerminal(featureID))
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print the mock's raw content to stdout instead of metadata")
	return cmd
}

func derefMockStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mockCollectionPath(featureID string) string {
	return "/api/features/" + url.PathEscape(featureID) + "/mocks"
}
