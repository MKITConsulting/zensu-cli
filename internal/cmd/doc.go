package cmd

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

func NewDocCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "doc",
		Aliases: []string{"docs"},
		Short:   "Generate documentation context and CLAUDE.md templates",
	}
	cmd.AddCommand(
		newDocClaudeMdCmd(f),
		newDocClaudeMdContextCmd(f),
		newDocGenContextCmd(f),
	)
	return cmd
}

func newDocClaudeMdCmd(f *Factory) *cobra.Command {
	var product, variant string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "claude-md",
		Short:        "Generate a CLAUDE.md template for a product",
		Long:         "Generate a CLAUDE.md template for a product. Templates: full (active feature dev), minimal (library/infra repos), ci-only (CI/CD integration only).",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			if variant == "" {
				return fmt.Errorf("--variant is required")
			}
			q := url.Values{}
			q.Set("variant", variant)
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/templates/claude-md?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = f.Out.Write(append(raw, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&variant, "variant", "", "template variant: full, minimal, or ci-only (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newDocClaudeMdContextCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "claude-md-context",
		Short:        "Get aggregated context used to generate a product's CLAUDE.md",
		Long:         "Get the aggregated context the server uses to generate a product's CLAUDE.md: product, components, features, tiers, and related metadata.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/claude-md-context", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func newDocGenContextCmd(f *Factory) *cobra.Command {
	var feature, docType string
	cmd := &cobra.Command{
		Use:          "gen-context <feature-id>",
		Short:        "Get aggregated context for generating a feature's documentation",
		Long:         "Get rich aggregated context for generating documentation for a feature. Returns feature details, subfeatures, source files, tests, security profile, tier availability, journeys, existing docs, component and product info. Valid doc types: user_facing, api_reference, tutorial, adr, release_notes, internal, migration_guide, overview.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := feature
			if len(args) == 1 {
				id = args[0]
			}
			if id == "" {
				return fmt.Errorf("a feature id is required (positional <feature-id> or --feature)")
			}
			if docType == "" {
				return fmt.Errorf("--doc-type is required")
			}
			q := url.Values{}
			q.Set("docType", docType)
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+id+"/doc-generation-context?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "feature ID (alternative to the positional argument)")
	cmd.Flags().StringVar(&docType, "doc-type", "", "documentation type: user_facing|api_reference|tutorial|adr|release_notes|internal|migration_guide|overview (required)")
	return cmd
}
