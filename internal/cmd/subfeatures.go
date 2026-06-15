package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func subfeatureFeatureID(flagValue string, args []string) (string, []string) {
	if flagValue != "" {
		return flagValue, args
	}
	if len(args) > 0 {
		return args[0], args[1:]
	}
	return "", args
}

func NewSubfeaturesCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subfeatures",
		Aliases: []string{"subfeature"},
		Short:   "Manage sub-features of a feature",
	}
	cmd.AddCommand(
		newSubfeaturesListCmd(f),
		newSubfeaturesAddCmd(f),
		newSubfeaturesPromoteCmd(f),
	)
	return cmd
}

func newSubfeaturesListCmd(f *Factory) *cobra.Command {
	var feature string
	var compact, asJSON bool
	cmd := &cobra.Command{
		Use:          "list [feature-id]",
		Short:        "List all direct sub-features of a feature",
		Long:         "List all direct sub-features of a feature. Returns an empty array if no sub-features exist.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, _ := subfeatureFeatureID(feature, args)
			if featureID == "" {
				return fmt.Errorf("--feature is required")
			}
			path := "/api/features/" + featureID + "/subfeatures"
			if compact {
				q := url.Values{}
				q.Set("view", "compact")
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
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
	cmd.Flags().StringVar(&feature, "feature", "", "parent feature ID (required; or pass as positional arg)")
	cmd.Flags().BoolVar(&compact, "compact", false, "compact view (id, slug, title, status, priority, componentId only)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newSubfeaturesAddCmd(f *Factory) *cobra.Command {
	var feature, title, slug, description, status, priority, assignee string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "add [feature-id]",
		Short: "Create a sub-feature under a parent feature",
		Long: "Create a sub-feature under a parent feature. Sub-features inherit the product context and can later be " +
			"promoted to standalone features via promote. Split a feature into subfeatures along: workflow steps, " +
			"alternative paths (happy vs. error), interface variations, or data variations. Subfeatures share the " +
			"parent's component and release timeline. If a subfeature diverges in component or schedule, promote it to " +
			"a top-level feature.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, _ := subfeatureFeatureID(feature, args)
			if featureID == "" {
				return fmt.Errorf("--feature is required")
			}
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			s := slug
			if s == "" {
				s = slugify(title)
			}
			if s == "" {
				return fmt.Errorf("could not derive a slug from --title; pass --slug explicitly")
			}
			payload := map[string]string{
				"slug":  s,
				"title": title,
			}
			if cmd.Flags().Changed("description") {
				payload["description"] = description
			}
			if cmd.Flags().Changed("status") {
				payload["status"] = status
			}
			if cmd.Flags().Changed("priority") {
				payload["priority"] = priority
			}
			if cmd.Flags().Changed("assignee") {
				payload["assignee"] = assignee
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+featureID+"/subfeatures", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var ft featureItem
			_ = json.Unmarshal(raw, &ft)
			_, err = fmt.Fprintf(f.Out, "Created sub-feature %s %s (%s)\n", ft.Slug, ft.Title, ft.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "parent feature ID (required; or pass as positional arg)")
	cmd.Flags().StringVar(&title, "title", "", "sub-feature title (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe identifier (derived from --title if omitted)")
	cmd.Flags().StringVar(&description, "description", "", "sub-feature description")
	cmd.Flags().StringVar(&status, "status", "", "initial status (default: planned)")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical|high|medium|low")
	cmd.Flags().StringVar(&assignee, "assignee", "", "assignee identifier")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newSubfeaturesPromoteCmd(f *Factory) *cobra.Command {
	var feature string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "promote [feature-id] <subfeature-id>",
		Short: "Promote a sub-feature to a standalone top-level feature",
		Long: "Promote a sub-feature to a standalone top-level feature. Removes the parent link. The feature retains " +
			"all other attributes.",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, rest := subfeatureFeatureID(feature, args)
			if featureID == "" {
				return fmt.Errorf("--feature is required")
			}
			if len(rest) < 1 {
				return fmt.Errorf("subfeature-id is required")
			}
			subID := rest[0]
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+featureID+"/subfeatures/"+subID+"/promote", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Promoted sub-feature %s to a top-level feature\n", subID)
			return err
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "parent feature ID (required; or pass as positional arg)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
