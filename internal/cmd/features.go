package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func slugify(s string) string {
	const maxSlug = 200
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if b.Len() >= maxSlug {
			break
		}
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

type featureItem struct {
	ID     string  `json:"id"`
	Slug   string  `json:"slug"`
	Title  string  `json:"title"`
	Status *string `json:"status"`
}

func NewFeaturesCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "features",
		Aliases: []string{"feature"},
		Short:   "Manage features",
	}
	cmd.AddCommand(
		newFeaturesListCmd(f),
		newFeaturesGetCmd(f),
		newFeaturesCreateCmd(f),
		newFeaturesUpdateCmd(f),
		newFeaturesStatusCmd(f),
		newFeaturesHistoryCmd(f),
		newFeaturesDeprecateCmd(f),
		newFeaturesSplitCmd(f),
		newFeaturesMergeCmd(f),
		newFeaturesRevisionCmd(f),
	)
	return cmd
}

func newFeaturesListCmd(f *Factory) *cobra.Command {
	var product, status string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List features in a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			q := url.Values{}
			q.Set("productId", product)
			if status != "" {
				q.Set("status", status)
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []featureItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ZEN\tTITLE\tSTATUS")
			for _, ft := range env.Data {
				st := ""
				if ft.Status != nil {
					st = *ft.Status
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", ft.Slug, ft.Title, st)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (planned|in-progress|testing|released)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesGetCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "get <feature-id>",
		Short:        "Get a feature by ID",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newFeaturesCreateCmd(f *Factory) *cobra.Command {
	var product, component, title, slug, status string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a feature",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" || component == "" || title == "" {
				return fmt.Errorf("--product, --component and --title are required")
			}
			s := slug
			if s == "" {
				s = slugify(title)
			}
			if s == "" {
				return fmt.Errorf("could not derive a slug from --title; pass --slug explicitly")
			}
			payload := map[string]string{
				"productId":   product,
				"componentId": component,
				"title":       title,
				"slug":        s,
			}
			if status != "" {
				payload["status"] = status
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var ft featureItem
			_ = json.Unmarshal(raw, &ft)
			_, err = fmt.Fprintf(f.Out, "Created feature %s %s (%s)\n", ft.Slug, ft.Title, ft.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&component, "component", "", "component ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "feature title (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "feature slug / ZEN id (derived from --title if omitted)")
	cmd.Flags().StringVar(&status, "status", "", "initial status")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesUpdateCmd(f *Factory) *cobra.Command {
	var title, description, priority string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "update <feature-id>",
		Short:        "Update a feature's metadata",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("description") && !cmd.Flags().Changed("priority") {
				return fmt.Errorf("nothing to update: pass --title, --description, or --priority")
			}
			cur, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0], nil)
			if err != nil {
				return err
			}
			var existing featureItem
			if err := json.Unmarshal(cur, &existing); err != nil {
				return err
			}
			if existing.Slug == "" {
				return fmt.Errorf("could not read current feature %s", args[0])
			}
			payload := map[string]string{"slug": existing.Slug, "title": existing.Title}
			if cmd.Flags().Changed("title") {
				payload["title"] = title
			}
			if cmd.Flags().Changed("description") {
				payload["description"] = description
			}
			if cmd.Flags().Changed("priority") {
				payload["priority"] = priority
			}
			if payload["title"] == "" {
				return fmt.Errorf("title cannot be empty")
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPatch, "/api/features/"+args[0], body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Updated feature %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&priority, "priority", "", "new priority (low|medium|high|critical)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesStatusCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "status <feature-id> <new-status>",
		Short:        "Transition a feature's status",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := json.Marshal(map[string]string{"status": args[1]})
			if err != nil {
				return err
			}
			_, err = f.request(cmd.Context(), http.MethodPatch, "/api/features/"+args[0]+"/status", body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Feature %s status → %s\n", args[0], args[1])
			return err
		},
	}
}

func newFeaturesHistoryCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "history <feature-id>",
		Short:        "Show a feature's full history timeline",
		Long:         "Get the complete history of a feature including status changes, revisions, lifecycle events and security changes. Returns a chronological timeline.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0]+"/history", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newFeaturesDeprecateCmd(f *Factory) *cobra.Command {
	var reason, replacement, removalPlannedAt string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "deprecate <feature-id>",
		Short:        "Deprecate a feature",
		Long:         "Deprecate a feature. Optionally link a replacement feature and set a planned removal date.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]string{}
			if reason != "" {
				payload["reason"] = reason
			}
			if replacement != "" {
				payload["replacementId"] = replacement
			}
			if removalPlannedAt != "" {
				payload["removalPlannedAt"] = removalPlannedAt
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/lifecycle/deprecate", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Deprecated feature %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason for deprecation")
	cmd.Flags().StringVar(&replacement, "replacement", "", "UUID of the replacement feature")
	cmd.Flags().StringVar(&removalPlannedAt, "removal-planned-at", "", "planned removal date (RFC3339)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesSplitCmd(f *Factory) *cobra.Command {
	var children, reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "split <feature-id>",
		Short:        "Split a feature into multiple child features",
		Long:         "Split a feature into multiple child features. The original feature is marked as 'split' and new child features are created. Use when a feature has grown too large.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if children == "" {
				return fmt.Errorf("--children is required")
			}
			var childList []map[string]any
			if err := json.Unmarshal([]byte(children), &childList); err != nil {
				return fmt.Errorf("--children must be a JSON array of {\"title\",\"slug\"} objects: %w", err)
			}
			payload := map[string]any{"children": childList}
			if reason != "" {
				payload["reason"] = reason
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/lifecycle/split", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Split feature %s into %d child feature(s)\n", args[0], len(childList))
			return err
		},
	}
	cmd.Flags().StringVar(&children, "children", "", "JSON array of child features, each with 'title' and 'slug' (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "reason for splitting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesMergeCmd(f *Factory) *cobra.Command {
	var sources, title, slug, reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "merge <feature-id>",
		Short:        "Merge source features into a target feature",
		Long:         "Merge multiple source features into a new target feature. Source features are marked as 'merged'. Use when separate features should be combined.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sources == "" || title == "" || slug == "" {
				return fmt.Errorf("--source, --title and --slug are required")
			}
			var sourceIDs []string
			if err := json.Unmarshal([]byte(sources), &sourceIDs); err != nil {
				return fmt.Errorf("--source must be a JSON array of feature UUIDs: %w", err)
			}
			payload := map[string]any{
				"sourceIds": sourceIDs,
				"title":     title,
				"slug":      slug,
			}
			if reason != "" {
				payload["reason"] = reason
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/lifecycle/merge", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Merged %d feature(s) into %s (%s)\n", len(sourceIDs), slug, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&sources, "source", "", "JSON array of source feature UUIDs to merge (required)")
	cmd.Flags().StringVar(&title, "title", "", "title for the merged feature (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "slug for the merged feature (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "reason for merging")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newFeaturesRevisionCmd(f *Factory) *cobra.Command {
	var scopeSummary, scopeDetails, targetRelease, estimatedEffort, assignee, createdBy string
	var coverageTarget float64
	var docsRequired, asJSON bool
	cmd := &cobra.Command{
		Use:          "revision <feature-id>",
		Short:        "Create a new revision (version) of a feature",
		Long:         "Create a new revision (version) of a feature. Each revision tracks scope changes, acceptance criteria and breaking changes. Revisions are auto-versioned (v1, v2, ...).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if scopeSummary == "" {
				return fmt.Errorf("--scope-summary is required")
			}
			payload := map[string]any{"scopeSummary": scopeSummary}
			if scopeDetails != "" {
				payload["scopeDetails"] = scopeDetails
			}
			if targetRelease != "" {
				payload["targetRelease"] = targetRelease
			}
			if estimatedEffort != "" {
				payload["estimatedEffort"] = estimatedEffort
			}
			if assignee != "" {
				payload["assignee"] = assignee
			}
			if cmd.Flags().Changed("coverage-target") {
				payload["coverageTarget"] = coverageTarget
			}
			if cmd.Flags().Changed("docs-required") {
				payload["docsRequired"] = docsRequired
			}
			if createdBy != "" {
				payload["createdBy"] = createdBy
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/revisions", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var rev struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			}
			_ = json.Unmarshal(raw, &rev)
			_, err = fmt.Fprintf(f.Out, "Created revision %s for feature %s (%s)\n", rev.Version, args[0], rev.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&scopeSummary, "scope-summary", "", "brief summary of the revision scope (required)")
	cmd.Flags().StringVar(&scopeDetails, "scope-details", "", "detailed scope description")
	cmd.Flags().StringVar(&targetRelease, "target-release", "", "target release version")
	cmd.Flags().StringVar(&estimatedEffort, "estimated-effort", "", "estimated effort: S|M|L|XL")
	cmd.Flags().StringVar(&assignee, "assignee", "", "assignee identifier")
	cmd.Flags().Float64Var(&coverageTarget, "coverage-target", 0, "coverage target percentage (0-100)")
	cmd.Flags().BoolVar(&docsRequired, "docs-required", false, "whether documentation is required for this revision")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "creator identifier: api|mcp|web-ui|github-sync")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
