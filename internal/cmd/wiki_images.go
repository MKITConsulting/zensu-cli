package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const wikiImageMaxBytes = 10 << 20
const wikiImageHelp = `Use local static PNG, JPEG or WebP files only; remote URLs are not fetched.
The server sanitizes uploads and returns reference=wiki-asset:<uuid>.
Insert ![Alternative text](wiki-asset:<uuid> "Optional caption"){width=50%} into Markdown.
Omit the width for automatic sizing, or use 1–100% or 1–4096px; height is proportional.
Keep the reference in the same product and explicitly save the page (private by default).
Uploading alone never publishes; unused uploads expire after 30 days.`

func newWikiImagesCmd(f *Factory) *cobra.Command {
	root := &cobra.Command{Use: "images", Short: "Upload secure Wiki images and read server limits", Long: wikiImageHelp}
	var policyProduct string
	policy := &cobra.Command{Use: "policy", Short: "Read current byte, pixel and dimension limits", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := wikiImagePath(policyProduct)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path+"/policy", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		}}
	policy.Flags().StringVar(&policyProduct, "product", "", "product UUID (required)")
	root.AddCommand(jsonByDefault(policy))
	var product, file string
	var asJSON bool
	upload := &cobra.Command{Use: "upload", Short: "Upload a local image without publishing it", Long: wikiImageHelp, Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := wikiImagePath(product)
			if err != nil {
				return err
			}
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			if strings.Contains(file, "://") || strings.HasPrefix(file, "data:") {
				return fmt.Errorf("--file must be a local file, not a URL")
			}
			info, err := os.Stat(file)
			if err != nil {
				return fmt.Errorf("opening image: %w", err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("image must be a regular local file")
			}
			if info.Size() == 0 || info.Size() > wikiImageMaxBytes {
				return fmt.Errorf("image must contain 1 to %d bytes", wikiImageMaxBytes)
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path+"/policy", nil)
			if err != nil {
				return err
			}
			var limits struct {
				MaxBytes int64 `json:"maxBytes"`
			}
			if json.Unmarshal(raw, &limits) != nil || limits.MaxBytes <= 0 || limits.MaxBytes > wikiImageMaxBytes {
				return fmt.Errorf("invalid image upload policy")
			}
			if info.Size() > limits.MaxBytes {
				return fmt.Errorf("image exceeds the server limit of %d bytes", limits.MaxBytes)
			}
			input, err := os.Open(file)
			if err != nil {
				return fmt.Errorf("opening image: %w", err)
			}
			defer input.Close()
			// Read a bounded snapshot; a growing file cannot bypass the policy.
			data, err := io.ReadAll(io.LimitReader(input, limits.MaxBytes+1))
			if err != nil {
				return fmt.Errorf("reading image: %w", err)
			}
			if len(data) == 0 || int64(len(data)) > limits.MaxBytes {
				return fmt.Errorf("image is empty or exceeds the server limit of %d bytes", limits.MaxBytes)
			}
			raw, err = f.requestWithContentType(cmd.Context(), http.MethodPost, path, "application/octet-stream", data)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var image struct {
				Reference string `json:"reference"`
				Width     int    `json:"width"`
				Height    int    `json:"height"`
			}
			if err = json.Unmarshal(raw, &image); err != nil {
				return fmt.Errorf("decoding upload result: %w", err)
			}
			_, err = fmt.Fprintf(f.Out, "Uploaded private image %s (%d × %d)\nSave it in a page in this product; uploading does not publish it.\n", sanitizeTerminal(image.Reference), image.Width, image.Height)
			return err
		}}
	upload.Flags().StringVar(&product, "product", "", "product UUID (required)")
	upload.Flags().StringVar(&file, "file", "", "local PNG, JPEG or WebP file (required)")
	upload.Flags().BoolVar(&asJSON, "json", false, "output upload metadata and managed reference as JSON")
	root.AddCommand(upload)
	return root
}

var wikiImageProductPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func wikiImagePath(product string) (string, error) {
	if strings.TrimSpace(product) == "" {
		return "", fmt.Errorf("--product is required")
	}
	if !wikiImageProductPattern.MatchString(product) {
		return "", fmt.Errorf("--product must be a UUID")
	}
	return "/api/wiki/images/" + product, nil
}
