package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/spf13/cobra"
)

// Capture is deliberately a different API from human decisions. Plugins share
// this typed command surface; neither credentials nor a caller-supplied user ID
// become proof that a human accepted a result.
func NewAIWorkCmd(f *Factory) *cobra.Command {
	root := &cobra.Command{Use: "ai-work", Short: "Capture work and result versions using your personal OAuth login",
		Long: "Capture work for My work and Organization. API keys and work-order session tokens are refused. Submission is not acceptance; review and decide the exact result in the web application."}
	var id, title, order string
	start := jsonByDefault(&cobra.Command{Use: "start", Short: "Declare an agreed work objective (retry with the same ID)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := aiWorkUUID(id); err != nil {
				return err
			}
			if strings.TrimSpace(title) == "" {
				return fmt.Errorf("--title is required")
			}
			body := map[string]any{"id": id, "title": title}
			if order != "" {
				if err := aiWorkUUID(order); err != nil {
					return err
				}
				body["workOrderId"] = order
			}
			return aiWorkRequest(f, cmd, http.MethodPost, "/api/ai-work/capture/units", body)
		}})
	start.Flags().StringVar(&id, "id", "", "stable work UUID (required; keep across retries)")
	start.Flags().StringVar(&title, "title", "", "agreed work objective, at most 160 characters")
	start.Flags().StringVar(&order, "work-order", "", "optional canonical work-order UUID")
	get := jsonByDefault(&cobra.Command{Use: "get <work-id>", Short: "Read the current result and authenticated capture history", Args: aiWorkIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return aiWorkRequest(f, cmd, http.MethodGet, "/api/ai-work/capture/units/"+args[0], nil)
		}})
	var event, artifact, criteria string
	var expected int32
	submit := jsonByDefault(&cobra.Command{Use: "submit <work-id>", Short: "Submit an immutable result version; never accepts it", Args: aiWorkIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := aiWorkUUID(event); err != nil {
				return err
			}
			if expected < 1 || strings.TrimSpace(artifact) == "" || strings.TrimSpace(criteria) == "" {
				return fmt.Errorf("--expected-version, --artifact and --criteria-version are required")
			}
			return aiWorkRequest(f, cmd, http.MethodPost, "/api/ai-work/capture/units/"+args[0]+"/submissions", map[string]any{"id": event, "expectedVersion": expected, "artifactRef": artifact, "criteriaVersion": criteria})
		}})
	submit.Flags().StringVar(&event, "event-id", "", "stable submission UUID (required; keep across retries)")
	submit.Flags().Int32Var(&expected, "expected-version", 0, "observed work version; stale writes conflict")
	submit.Flags().StringVar(&artifact, "artifact", "", "immutable commit or document version reference")
	submit.Flags().StringVar(&criteria, "criteria-version", "", "version or digest of the agreed acceptance criteria")
	contextCmd := jsonByDefault(&cobra.Command{Use: "context", Short: "Show the selected capture host without credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := f.NewClient(cmd.Context())
		if err != nil {
			return err
		}
		if c.AuthMode() != client.AuthModeLogin {
			return fmt.Errorf("AI work capture requires a personal OAuth login")
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("AI work capture requires an HTTP(S) API URL without credentials, query or fragment")
		}
		b, err := json.Marshal(map[string]string{"apiUrl": c.BaseURL})
		if err != nil {
			return err
		}
		return printJSON(f.Out, b)
	}})
	root.AddCommand(start, get, submit, contextCmd)
	return root
}

func aiWorkUUID(id string) error {
	if !pulseSessionIDPattern.MatchString(id) || id == nilPulseSessionID {
		return fmt.Errorf("expected a canonical non-nil work/event UUID")
	}
	return nil
}
func aiWorkIDArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	return aiWorkUUID(args[0])
}
func aiWorkRequest(f *Factory, cmd *cobra.Command, method, path string, body any) error {
	c, err := f.NewClient(cmd.Context())
	if err != nil {
		return err
	}
	if c.AuthMode() != client.AuthModeLogin {
		return fmt.Errorf("AI work capture requires `zensu auth login` with a personal OAuth account; API keys and worker credentials cannot attribute personal work")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	raw, err := readResponse(c.Do(cmd.Context(), method, path, data))
	if err != nil {
		return err
	}
	return printJSON(f.Out, raw)
}
