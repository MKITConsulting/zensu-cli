package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func NewLinkCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link tests, docs, and source files to a feature",
	}
	cmd.AddCommand(
		newLinkTestCmd(f),
		newLinkDocsCmd(f),
		newLinkSourceCmd(f),
	)
	return cmd
}

func newLinkTestCmd(f *Factory) *cobra.Command {
	var testType, filePath, functionName, lastRunStatus string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "test <feature-id>",
		Short: "Link a test file to a feature",
		Long: "Link a test file to a feature. Enables tracking which tests cover a feature. " +
			"Use after writing tests to associate them with the feature.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if testType == "" {
				return fmt.Errorf("--test-type is required")
			}
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}
			payload := map[string]string{
				"testType": testType,
				"filePath": filePath,
			}
			if functionName != "" {
				payload["functionName"] = functionName
			}
			if lastRunStatus != "" {
				payload["lastRunStatus"] = lastRunStatus
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/tests", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Linked %s test %s to feature %s\n", testType, filePath, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&testType, "test-type", "", "test type: unit|integration|e2e|security|performance|accessibility (required)")
	cmd.Flags().StringVar(&filePath, "file", "", "path to the test file (required)")
	cmd.Flags().StringVar(&functionName, "function", "", "specific test function name")
	cmd.Flags().StringVar(&lastRunStatus, "last-run-status", "", "last run status: passed|failed|skipped")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newLinkDocsCmd(f *Factory) *cobra.Command {
	var docType, title, filePath, externalURL, audience, publicationStatus, content string
	var publishToWiki, asJSON bool
	cmd := &cobra.Command{
		Use:   "docs <feature-id>",
		Short: "Link documentation to a feature",
		Long: "Link documentation to a feature. Automatically updates the feature's docs score. " +
			"Use after creating or updating docs.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if docType == "" {
				return fmt.Errorf("--doc-type is required")
			}
			payload := map[string]any{
				"docType": docType,
			}
			if title != "" {
				payload["title"] = title
			}
			if filePath != "" {
				payload["filePath"] = filePath
			}
			if externalURL != "" {
				payload["externalUrl"] = externalURL
			}
			if audience != "" {
				payload["audience"] = audience
			}
			if publicationStatus != "" {
				payload["publicationStatus"] = publicationStatus
			}
			if content != "" {
				payload["content"] = content
			}
			if cmd.Flags().Changed("publish-to-wiki") {
				payload["publishToWiki"] = publishToWiki
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/docs", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Linked %s doc to feature %s\n", docType, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&docType, "doc-type", "", "doc type: user_facing|api_reference|tutorial|adr|internal|release_notes|migration_guide|overview (required)")
	cmd.Flags().StringVar(&title, "title", "", "document title")
	cmd.Flags().StringVar(&filePath, "file", "", "path to the doc file")
	cmd.Flags().StringVar(&externalURL, "external-url", "", "external URL for the doc")
	cmd.Flags().StringVar(&audience, "audience", "", "target audience: end_user|developer|admin|internal")
	cmd.Flags().StringVar(&publicationStatus, "publication-status", "", "publication status: draft|published|archived")
	cmd.Flags().StringVar(&content, "content", "", "wiki page content (markdown); creates/updates the linked wiki page when provided")
	cmd.Flags().BoolVar(&publishToWiki, "publish-to-wiki", true, "ensure a linked wiki page exists for this doc (default true)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newLinkSourceCmd(f *Factory) *cobra.Command {
	var files []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "source <feature-id>",
		Short: "Map source code files to a feature",
		Long: "Map source code files to a feature for documentation generation and change tracking. " +
			"Repeat --file for each source file; each value is path[:type[:language]].",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(files) == 0 {
				return fmt.Errorf("--file is required (repeat for multiple files)")
			}
			type sourceFile struct {
				FilePath  string `json:"filePath"`
				FileType  string `json:"fileType,omitempty"`
				Language  string `json:"language,omitempty"`
				LineCount int    `json:"lineCount,omitempty"`
			}
			entries := make([]sourceFile, 0, len(files))
			for _, raw := range files {
				parts := strings.SplitN(raw, ":", 4)
				if parts[0] == "" {
					return fmt.Errorf("--file value %q has an empty path", raw)
				}
				e := sourceFile{FilePath: parts[0]}
				if len(parts) > 1 {
					e.FileType = parts[1]
				}
				if len(parts) > 2 {
					e.Language = parts[2]
				}
				if len(parts) > 3 && parts[3] != "" {
					n, err := strconv.Atoi(parts[3])
					if err != nil {
						return fmt.Errorf("--file value %q has a non-numeric line count %q", raw, parts[3])
					}
					e.LineCount = n
				}
				entries = append(entries, e)
			}
			body, err := json.Marshal(map[string]any{"files": entries})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/source-files/bulk", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var resp struct {
				Created int `json:"created"`
				Updated int `json:"updated"`
			}
			_ = json.Unmarshal(raw, &resp)
			_, err = fmt.Fprintf(f.Out, "Linked %d source file(s) to feature %s (created %d, updated %d)\n", len(entries), args[0], resp.Created, resp.Updated)
			return err
		},
	}
	cmd.Flags().StringArrayVar(&files, "file", nil, "source file as path[:type[:language]] (repeatable, required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
