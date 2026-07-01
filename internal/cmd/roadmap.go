package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type roadmapItem struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Period *string `json:"period"`
	Status *string `json:"status"`
}

type milestoneItem struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Period *string `json:"period"`
	Status *string `json:"status"`
}

func NewRoadmapCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roadmap",
		Aliases: []string{"roadmaps"},
		Short:   "Manage product roadmaps",
	}
	cmd.AddCommand(
		newRoadmapListCmd(f),
		newRoadmapGetCmd(f),
		newRoadmapCreateCmd(f),
		newRoadmapUpdateCmd(f),
		newRoadmapDeleteCmd(f),
		newRoadmapAddFeatureCmd(f),
		newRoadmapRemoveFeatureCmd(f),
		newRoadmapMilestoneCreateCmd(f),
		newRoadmapMilestoneListCmd(f),
		newRoadmapMilestoneDeleteCmd(f),
	)
	return cmd
}

func newRoadmapListCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List all roadmaps for a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/roadmaps", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []roadmapItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTITLE\tPERIOD\tSTATUS")
			for _, r := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Title, roadmapDeref(r.Period), roadmapDeref(r.Status))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newRoadmapGetCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "get <roadmap-id>",
		Short:        "Get a roadmap with its embedded features and milestones",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/roadmaps/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newRoadmapCreateCmd(f *Factory) *cobra.Command {
	var product, title, period, description, status string
	var goals []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a roadmap for a product",
		Long:         "Create a roadmap for a product: a named timeline (e.g. 'MVP Launch'). ONE roadmap spans many quarters — do not create one per quarter. After creating, add features with `roadmap add-feature` (each with --start-period/--end-period so it spans quarters) and key dates with `roadmap milestone-create`. The --period is the anchor/start quarter.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" || title == "" {
				return fmt.Errorf("--product and --title are required")
			}
			payload := map[string]any{"title": title}
			if period != "" {
				payload["period"] = period
			}
			if description != "" {
				payload["description"] = description
			}
			if status != "" {
				payload["status"] = status
			}
			if len(goals) > 0 {
				payload["goals"] = goals
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/roadmaps", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var r roadmapItem
			_ = json.Unmarshal(raw, &r)
			_, err = fmt.Fprintf(f.Out, "Created roadmap %s (%s)\n", r.Title, r.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "roadmap title (required)")
	cmd.Flags().StringVar(&period, "period", "", "anchor/start period — quarter (2026-Q2), month (2026-06), week (2026-W23), or day (2026-06-15)")
	cmd.Flags().StringVar(&description, "description", "", "roadmap description")
	cmd.Flags().StringArrayVar(&goals, "goal", nil, "goal string (repeatable)")
	cmd.Flags().StringVar(&status, "status", "", "roadmap status: draft|active|completed|archived (default draft)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newRoadmapUpdateCmd(f *Factory) *cobra.Command {
	var title, period, description, status string
	var goals []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "update <roadmap-id>",
		Short:        "Update a roadmap's title, period, description, goals or status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			payload := map[string]any{"title": title}
			if cmd.Flags().Changed("period") {
				payload["period"] = period
			}
			if cmd.Flags().Changed("description") {
				payload["description"] = description
			}
			if cmd.Flags().Changed("status") {
				payload["status"] = status
			}
			if cmd.Flags().Changed("goal") {
				payload["goals"] = goals
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPut, "/api/roadmaps/"+args[0], body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Updated roadmap %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "roadmap title (required)")
	cmd.Flags().StringVar(&period, "period", "", "time period — quarter (2026-Q2), month (2026-06), week (2026-W23), or day (2026-06-15)")
	cmd.Flags().StringVar(&description, "description", "", "roadmap description")
	cmd.Flags().StringArrayVar(&goals, "goal", nil, "goal string (repeatable)")
	cmd.Flags().StringVar(&status, "status", "", "roadmap status: draft|active|completed|archived")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newRoadmapDeleteCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "delete <roadmap-id>",
		Short:        "Delete a roadmap (linked features are not deleted, only their membership)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := f.request(cmd.Context(), http.MethodDelete, "/api/roadmaps/"+args[0], nil)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Deleted roadmap %s\n", args[0])
			return err
		},
	}
}

func newRoadmapAddFeatureCmd(f *Factory) *cobra.Command {
	var feature, startPeriod, endPeriod string
	var sortOrder int
	cmd := &cobra.Command{
		Use:          "add-feature <roadmap-id>",
		Short:        "Add an existing feature to a roadmap",
		Long:         "Add an existing feature to a roadmap. Set --start-period AND --end-period to the periods the feature spans (e.g. '2026-Q2'..'2026-Q4') — this draws its timeline bar; omit them and every feature collapses into one column. Single-period feature: set both to the same value. Idempotent: re-adding updates sort order and periods.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if feature == "" {
				return fmt.Errorf("--feature is required")
			}
			payload := map[string]any{"featureId": feature}
			if cmd.Flags().Changed("sort-order") {
				payload["sortOrder"] = sortOrder
			}
			if startPeriod != "" {
				payload["startPeriod"] = startPeriod
			}
			if endPeriod != "" {
				payload["endPeriod"] = endPeriod
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			_, err = f.request(cmd.Context(), http.MethodPost, "/api/roadmaps/"+args[0]+"/features", body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Added feature %s to roadmap %s\n", feature, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "feature ID (required)")
	cmd.Flags().IntVar(&sortOrder, "sort-order", 0, "sort order within the roadmap (ascending, default 0)")
	cmd.Flags().StringVar(&startPeriod, "start-period", "", "start period for the timeline bar — quarter (2026-Q2), month (2026-06), week (2026-W23), or day (2026-06-15)")
	cmd.Flags().StringVar(&endPeriod, "end-period", "", "end period for the timeline bar — quarter (2026-Q4), month (2026-12), week (2026-W42), or day (2026-12-31)")
	return cmd
}

func newRoadmapRemoveFeatureCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "remove-feature <roadmap-id> <feature-id>",
		Short:        "Remove a feature from a roadmap (the feature itself is not deleted)",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := f.request(cmd.Context(), http.MethodDelete, "/api/roadmaps/"+args[0]+"/features/"+args[1], nil)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Removed feature %s from roadmap %s\n", args[1], args[0])
			return err
		},
	}
}

func newRoadmapMilestoneCreateCmd(f *Factory) *cobra.Command {
	var title, period, status string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "milestone-create <roadmap-id>",
		Short:        "Create a milestone marker on a roadmap",
		Long:         "Create a milestone marker on a roadmap (e.g. a release or key date), positioned at a period on the timeline (quarter, month, ISO week or day — match the roadmap's granularity).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			payload := map[string]any{"title": title}
			if period != "" {
				payload["period"] = period
			}
			if status != "" {
				payload["status"] = status
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/roadmaps/"+args[0]+"/milestones", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var m milestoneItem
			_ = json.Unmarshal(raw, &m)
			_, err = fmt.Fprintf(f.Out, "Created milestone %s (%s)\n", m.Title, m.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "milestone title (required), e.g. 'GA Release'")
	cmd.Flags().StringVar(&period, "period", "", "period the milestone sits in — quarter (2026-Q3), month (2026-06), week (2026-W23), or day (2026-06-15)")
	cmd.Flags().StringVar(&status, "status", "", "milestone status, e.g. planned|done")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newRoadmapMilestoneListCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "milestone-list <roadmap-id>",
		Short:        "List all milestones for a roadmap",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/roadmaps/"+args[0]+"/milestones", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var ms []milestoneItem
			if err := json.Unmarshal(raw, &ms); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTITLE\tPERIOD\tSTATUS")
			for _, m := range ms {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Title, roadmapDeref(m.Period), roadmapDeref(m.Status))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newRoadmapMilestoneDeleteCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "milestone-delete <roadmap-id> <milestone-id>",
		Short:        "Delete a milestone from a roadmap",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := f.request(cmd.Context(), http.MethodDelete, "/api/roadmaps/"+args[0]+"/milestones/"+args[1], nil)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Deleted milestone %s from roadmap %s\n", args[1], args[0])
			return err
		},
	}
}

func roadmapDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
