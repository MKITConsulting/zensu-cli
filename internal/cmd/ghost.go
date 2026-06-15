package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

func NewGhostCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ghost",
		Short: "Manage Ghost scans and feature candidates",
	}
	cmd.AddCommand(
		newGhostScanCmd(f),
		newGhostCandidatesCmd(f),
		newGhostApproveCmd(f),
		newGhostRejectCmd(f),
		newGhostBatchCmd(f),
		newGhostApplyCmd(f),
	)
	return cmd
}

func newGhostScanCmd(f *Factory) *cobra.Command {
	var product, candidates, components, repoURL, branch string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "scan",
		Short:        "Create a Ghost scan with discovered feature candidates",
		Long:         "Create a Ghost scan with feature candidates discovered from repository analysis (optionally deepened by a multi-perspective fan-out of read-only analysis lenses to raise recall). Candidates are created in 'pending' review status for user approval. Populate detectedSourceFiles, detectedTestFiles, and detectedDocFiles per candidate - tests and docs are first-class scan data, linked on apply.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			if candidates == "" {
				return fmt.Errorf("--candidates is required")
			}
			var candidatesData json.RawMessage
			if err := json.Unmarshal([]byte(candidates), &candidatesData); err != nil {
				return fmt.Errorf("--candidates must be a JSON array: %w", err)
			}
			payload := map[string]any{
				"candidates": candidatesData,
				"source":     "cli",
			}
			if components != "" {
				var componentsData json.RawMessage
				if err := json.Unmarshal([]byte(components), &componentsData); err != nil {
					return fmt.Errorf("--components must be a JSON array: %w", err)
				}
				payload["components"] = componentsData
			}
			if repoURL != "" {
				payload["repoUrl"] = repoURL
			}
			if branch != "" {
				payload["branch"] = branch
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/ghost/scans", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Scan struct {
					ID              string `json:"id"`
					CandidatesTotal int    `json:"candidates_total"`
				} `json:"scan"`
			}
			_ = json.Unmarshal(raw, &env)
			_, err = fmt.Fprintf(f.Out, "Created ghost scan %s (%d candidates)\n", env.Scan.ID, env.Scan.CandidatesTotal)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&candidates, "candidates", "", "JSON array of candidate objects (required)")
	cmd.Flags().StringVar(&components, "components", "", "optional JSON array of component objects")
	cmd.Flags().StringVar(&repoURL, "repo-url", "", "repository URL")
	cmd.Flags().StringVar(&branch, "branch", "", "branch name")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newGhostCandidatesCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "candidates <scan-id>",
		Short:        "List feature candidates for a Ghost scan, ordered by confidence",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/ghost/scans/"+args[0]+"/candidates", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func newGhostApproveCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "approve <scan-id> <candidate-id>",
		Short:        "Approve a Ghost feature candidate for creation as a real feature",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/ghost/scans/"+args[0]+"/candidates/"+args[1]+"/approve", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Approved candidate %s\n", args[1])
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newGhostRejectCmd(f *Factory) *cobra.Command {
	var product, reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "reject <scan-id> <candidate-id>",
		Short:        "Reject a Ghost feature candidate with optional reason",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			payload := map[string]any{}
			if reason != "" {
				payload["reason"] = reason
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/ghost/scans/"+args[0]+"/candidates/"+args[1]+"/reject", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Rejected candidate %s\n", args[1])
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "rejection reason")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newGhostBatchCmd(f *Factory) *cobra.Command {
	var product, approveIDs, rejectIDs, rejectReason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "batch <scan-id>",
		Short:        "Batch approve and/or reject multiple Ghost candidates in one call",
		Long:         "Batch approve and/or reject multiple Ghost candidates in a single call. Provide JSON arrays of candidate IDs to approve and reject. Much more efficient than approving/rejecting candidates individually.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			var approve []string
			if approveIDs != "" {
				if err := json.Unmarshal([]byte(approveIDs), &approve); err != nil {
					return fmt.Errorf("--approve-ids must be a JSON array of UUIDs: %w", err)
				}
			}
			var rejectList []string
			if rejectIDs != "" {
				if err := json.Unmarshal([]byte(rejectIDs), &rejectList); err != nil {
					return fmt.Errorf("--reject-ids must be a JSON array of UUIDs: %w", err)
				}
			}
			if len(approve) == 0 && len(rejectList) == 0 {
				return fmt.Errorf("at least one of --approve-ids or --reject-ids is required")
			}
			reject := make([]map[string]any, 0, len(rejectList))
			for _, id := range rejectList {
				item := map[string]any{"id": id}
				if rejectReason != "" {
					item["reason"] = rejectReason
				}
				reject = append(reject, item)
			}
			payload := map[string]any{
				"approve": approve,
				"reject":  reject,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/ghost/scans/"+args[0]+"/batch-review", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				Approved int `json:"approved"`
				Rejected int `json:"rejected"`
			}
			_ = json.Unmarshal(raw, &res)
			_, err = fmt.Fprintf(f.Out, "Batch review complete: %d approved, %d rejected\n", res.Approved, res.Rejected)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&approveIDs, "approve-ids", "", "JSON array of candidate UUIDs to approve")
	cmd.Flags().StringVar(&rejectIDs, "reject-ids", "", "JSON array of candidate UUIDs to reject")
	cmd.Flags().StringVar(&rejectReason, "reject-reason", "", "reason for rejecting candidates (applies to all rejected)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newGhostApplyCmd(f *Factory) *cobra.Command {
	var product string
	var enrichExisting, asJSON bool
	cmd := &cobra.Command{
		Use:          "apply <scan-id>",
		Short:        "Apply all approved Ghost candidates as real features",
		Long:         "Apply all approved Ghost candidates as real features. Creates features, components, and links test/doc/source files. Set --enrich-existing when the product already has features defined - this links discovered tests, docs, and source files to matching existing features (by slug) instead of failing with a conflict error. Use --enrich-existing by default unless this is the very first scan on an empty product.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			path := "/api/products/" + product + "/ghost/scans/" + args[0] + "/apply"
			if enrichExisting {
				path += "?enrich_existing=true"
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, path, nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				FeaturesCreated   int `json:"featuresCreated"`
				FeaturesEnriched  int `json:"featuresEnriched"`
				ComponentsCreated int `json:"componentsCreated"`
			}
			_ = json.Unmarshal(raw, &res)
			_, err = fmt.Fprintf(f.Out, "Applied scan %s: %d features created, %d enriched, %d components created\n", args[0], res.FeaturesCreated, res.FeaturesEnriched, res.ComponentsCreated)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&enrichExisting, "enrich-existing", false, "enrich matching existing features instead of failing on slug conflict")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
