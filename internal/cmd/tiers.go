package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type tierItem struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	TierOrder int     `json:"tier_order"`
	IsDefault *bool   `json:"is_default"`
	Color     *string `json:"color"`
}

type featureTierEntry struct {
	TierID     string `json:"tierId"`
	GatingType string `json:"gatingType"`
	TierLimits any    `json:"tierLimits,omitempty"`
}

func NewTiersCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tiers",
		Aliases: []string{"tier"},
		Short:   "Manage pricing tiers and feature tier availability",
	}
	cmd.AddCommand(
		newTiersCreateCmd(f),
		newTiersListCmd(f),
		newTiersMatrixCmd(f),
		newTiersSetFeatureCmd(f),
	)
	return cmd
}

func newTiersCreateCmd(f *Factory) *cobra.Command {
	var product, slug, name, description, color string
	var tierOrder int
	var isDefault, asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a pricing tier for a product",
		Long:         "Create a pricing tier for a product (e.g. Free, Pro, Enterprise). Required before using set-feature.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			if slug == "" {
				return fmt.Errorf("--slug is required")
			}
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if !cmd.Flags().Changed("tier-order") {
				return fmt.Errorf("--tier-order is required")
			}
			payload := map[string]any{
				"slug":      slug,
				"name":      name,
				"tierOrder": tierOrder,
			}
			if description != "" {
				payload["description"] = description
			}
			if cmd.Flags().Changed("default") {
				payload["isDefault"] = isDefault
			}
			if color != "" {
				payload["color"] = color
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/tiers", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var tr tierItem
			_ = json.Unmarshal(raw, &tr)
			_, err = fmt.Fprintf(f.Out, "Created tier %s %s (%s)\n", tr.Slug, tr.Name, tr.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe identifier, e.g. free, pro, enterprise (required)")
	cmd.Flags().StringVar(&name, "name", "", "display name, e.g. Free, Pro, Enterprise (required)")
	cmd.Flags().IntVar(&tierOrder, "tier-order", 0, "sort order, 1=lowest tier, ascending (required)")
	cmd.Flags().StringVar(&description, "description", "", "tier description")
	cmd.Flags().BoolVar(&isDefault, "default", false, "whether this is the default tier")
	cmd.Flags().StringVar(&color, "color", "", "display color for the tier")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newTiersListCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List all pricing tiers for a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/tiers", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []tierItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ORDER\tSLUG\tNAME\tDEFAULT")
			for _, tr := range env.Data {
				def := ""
				if tr.IsDefault != nil && *tr.IsDefault {
					def = "yes"
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", tr.TierOrder, tr.Slug, tr.Name, def)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newTiersMatrixCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "matrix",
		Short:        "Get the complete tier matrix for a product",
		Long:         "Get the complete tier matrix for a product showing which features are available in which tiers, including gating types and limits.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/tier-matrix", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func newTiersSetFeatureCmd(f *Factory) *cobra.Command {
	var tiers string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "set-feature <feature-id>",
		Short:        "Set the tier availability for a feature",
		Long:         "Set the tier availability for a feature. Replaces all existing tier assignments. Each entry specifies a tier, gating type (hard/soft/preview) and optional limits.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tiers == "" {
				return fmt.Errorf("--tiers is required")
			}
			var entries []featureTierEntry
			if err := json.Unmarshal([]byte(tiers), &entries); err != nil {
				return fmt.Errorf("invalid --tiers JSON: %w", err)
			}
			body, err := json.Marshal(map[string]any{"tiers": entries})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPut, "/api/features/"+args[0]+"/tiers", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Set %d tier assignment(s) for feature %s\n", len(entries), args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&tiers, "tiers", "", "JSON array of tier entries, each with 'tierId', 'gatingType' (hard|soft|preview), and optional 'tierLimits' object (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
