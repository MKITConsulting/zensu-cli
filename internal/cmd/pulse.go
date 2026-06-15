package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

func NewPulseCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pulse",
		Short: "Track development sessions",
	}
	cmd.AddCommand(
		newPulseStartCmd(f),
		newPulseEndCmd(f),
		newPulseSummaryCmd(f),
	)
	return cmd
}

func newPulseStartCmd(f *Factory) *cobra.Command {
	var headSha, branch, project, product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "start",
		Short:        "Start a new development session",
		Long:         "Start a new development session. Call at the beginning of a coding session with the current git HEAD SHA. Sessions are idempotent — calling with the same head_sha returns the existing session.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if headSha == "" {
				return fmt.Errorf("--head-sha is required")
			}
			payload := map[string]string{"headSha": headSha}
			if branch != "" {
				payload["branch"] = branch
			}
			if project != "" {
				payload["projectPath"] = project
			}
			if product != "" {
				payload["productId"] = product
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/pulse/sessions", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var sess struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &sess)
			_, err = fmt.Fprintf(f.Out, "Started session %s\n", sess.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&headSha, "head-sha", "", "current git HEAD SHA, short or full (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "current git branch name")
	cmd.Flags().StringVar(&project, "project", "", "absolute path to the project root")
	cmd.Flags().StringVar(&product, "product", "", "Zensu product UUID to associate with this session")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPulseEndCmd(f *Factory) *cobra.Command {
	var changedFiles string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "end <session-id>",
		Short:        "End a development session",
		Long:         "End a development session. Call when wrapping up work. Provide changed files from 'git diff --name-only' to automatically map which features were touched.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			files := []string{}
			if changedFiles != "" {
				for _, p := range strings.Split(changedFiles, ",") {
					if trimmed := strings.TrimSpace(p); trimmed != "" {
						files = append(files, trimmed)
					}
				}
			}
			body, err := json.Marshal(map[string]any{"changedFiles": files})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/pulse/sessions/"+args[0]+"/end", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Ended session %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&changedFiles, "changed-files", "", "comma-separated list of changed file paths (from git diff --name-only)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPulseSummaryCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "summary <session-id>",
		Short:        "Get a summary of a development session",
		Long:         "Get a summary of a development session including all tool calls made during the session.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/pulse/sessions/"+args[0]+"/summary", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}
