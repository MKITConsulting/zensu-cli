package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const pulseTrackingDisabledStatus = "tracking_disabled"

var (
	errPulseRequestFailed   = errors.New("Pulse request failed")
	errInvalidPulseResponse = errors.New("invalid Pulse response")
)

var pulseSessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type pulseCommandResponse struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

func normalizePulseSessionID(raw string) (string, error) {
	normalized := strings.ToLower(raw)
	if !pulseSessionIDPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid session id %q: expected canonical UUID", raw)
	}
	return normalized, nil
}

func parsePulseCommandResponse(raw []byte) (pulseCommandResponse, error) {
	var response pulseCommandResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return pulseCommandResponse{}, fmt.Errorf("invalid Pulse response: %w", err)
	}
	if response.Status == pulseTrackingDisabledStatus {
		if response.ID != "" {
			return pulseCommandResponse{}, fmt.Errorf("unexpected Pulse response: tracking_disabled must not include a session id")
		}
		return response, nil
	}
	if response.Status != "" {
		return pulseCommandResponse{}, fmt.Errorf("unexpected Pulse response status %q", response.Status)
	}
	if response.ID == "" {
		return pulseCommandResponse{}, fmt.Errorf("unexpected Pulse response: missing session id")
	}
	normalizedID, err := normalizePulseSessionID(response.ID)
	if err != nil {
		return pulseCommandResponse{}, fmt.Errorf("unexpected Pulse response: %w", err)
	}
	response.ID = normalizedID
	return response, nil
}

func printMinimalPulseCommandResponse(f *Factory, response pulseCommandResponse) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return printJSON(f.Out, raw)
}

func pulseRequestError(minimalJSON bool, err error) error {
	if minimalJSON {
		return errPulseRequestFailed
	}
	return err
}

func pulseResponseError(minimalJSON bool, err error) error {
	if minimalJSON {
		return errInvalidPulseResponse
	}
	return err
}

func pulseSessionIDArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	normalizedID, err := normalizePulseSessionID(args[0])
	if err != nil {
		return err
	}
	args[0] = normalizedID
	return nil
}

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
	var asJSON, minimalJSON bool
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
				return pulseRequestError(minimalJSON, err)
			}
			response, err := parsePulseCommandResponse(raw)
			if err != nil {
				return pulseResponseError(minimalJSON, err)
			}
			if minimalJSON {
				return printMinimalPulseCommandResponse(f, response)
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			if response.Status == pulseTrackingDisabledStatus {
				_, err = fmt.Fprintln(f.Out, "Pulse tracking is disabled in Zensu; no session was created.")
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Started session %s\n", response.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&headSha, "head-sha", "", "current git HEAD SHA, short or full (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "current git branch name")
	cmd.Flags().StringVar(&project, "project", "", "absolute path to the project root")
	cmd.Flags().StringVar(&product, "product", "", "Zensu product UUID to associate with this session")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	cmd.Flags().BoolVar(&minimalJSON, "minimal-json", false, "output only the session id or tracking-disabled status")
	cmd.MarkFlagsMutuallyExclusive("json", "minimal-json")
	return cmd
}

func newPulseEndCmd(f *Factory) *cobra.Command {
	var changedFiles []string
	var legacyChangedFiles string
	var asJSON, minimalJSON bool
	cmd := &cobra.Command{
		Use:          "end <session-id>",
		Short:        "End a development session",
		Long:         "End a development session. Call when wrapping up work. Provide changed files from 'git diff --name-only' to automatically map which features were touched.",
		Args:         pulseSessionIDArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			files := append([]string{}, changedFiles...)
			if legacyChangedFiles != "" {
				for _, p := range strings.Split(legacyChangedFiles, ",") {
					if trimmed := strings.TrimSpace(p); trimmed != "" {
						files = append(files, trimmed)
					}
				}
			}
			body, err := json.Marshal(map[string]any{"changedFiles": files})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/pulse/sessions/"+url.PathEscape(args[0])+"/end", body)
			if err != nil {
				return pulseRequestError(minimalJSON, err)
			}
			response, err := parsePulseCommandResponse(raw)
			if err != nil {
				return pulseResponseError(minimalJSON, err)
			}
			if response.Status != pulseTrackingDisabledStatus && response.ID != args[0] {
				return pulseResponseError(minimalJSON, fmt.Errorf(
					"unexpected Pulse response: session id %q does not match requested session id %q",
					response.ID, args[0],
				))
			}
			if minimalJSON {
				return printMinimalPulseCommandResponse(f, response)
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			if response.Status == pulseTrackingDisabledStatus {
				_, err = fmt.Fprintln(f.Out, "Pulse tracking is disabled in Zensu; no session end was recorded.")
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Ended session %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringArrayVar(&changedFiles, "changed-file", nil, "changed file path; repeat for each path (lossless, preferred)")
	cmd.Flags().StringVar(&legacyChangedFiles, "changed-files", "", "legacy comma-separated list of changed file paths")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	cmd.Flags().BoolVar(&minimalJSON, "minimal-json", false, "output only the session id or tracking-disabled status")
	cmd.MarkFlagsMutuallyExclusive("json", "minimal-json")
	return cmd
}

func newPulseSummaryCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "summary <session-id>",
		Short:        "Get a summary of a development session",
		Long:         "Get a summary of a development session including all tool calls made during the session.",
		Args:         pulseSessionIDArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/pulse/sessions/"+url.PathEscape(args[0])+"/summary", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}
