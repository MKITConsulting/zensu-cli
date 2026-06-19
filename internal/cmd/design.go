package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type designContextEntry struct {
	Scope   string `json:"scope"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type designImageEntry struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

type designContext struct {
	ProductID string               `json:"productId"`
	DesignMd  []designContextEntry `json:"designMd"`
	CSS       []designContextEntry `json:"css"`
	Images    []designImageEntry   `json:"images"`
}

func NewDesignCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "design",
		Short: "Inspect a product's design system",
		Long: "Inspect a product's design system: Design.md prose, shared CSS and image assets, scoped to the " +
			"product or an individual component.",
	}
	cmd.AddCommand(
		newDesignContextCmd(f),
	)
	return cmd
}

func newDesignContextCmd(f *Factory) *cobra.Command {
	var component string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "context <product-id>",
		Short: "Get a product's aggregated design context",
		Long: "Get a product's aggregated design context: Design.md markdown, shared CSS and image asset metadata. " +
			"Pass --component to additionally include a component's scoped overrides. Use this to load a product's " +
			"design system into your working context before building UI.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/products/" + args[0] + "/design/context"
			if component != "" {
				q := url.Values{}
				q.Set("componentId", component)
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var ctx designContext
			if err := json.Unmarshal(raw, &ctx); err != nil {
				return err
			}
			return printDesignContext(f, ctx)
		},
	}
	cmd.Flags().StringVar(&component, "component", "", "component ID to include component-scoped overrides")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func printDesignContext(f *Factory, ctx designContext) error {
	for _, md := range ctx.DesignMd {
		if _, err := fmt.Fprintf(f.Out, "# %s (%s)\n\n%s\n\n", md.Name, md.Scope, md.Content); err != nil {
			return err
		}
	}
	for _, css := range ctx.CSS {
		if _, err := fmt.Fprintf(f.Out, "/* %s (%s) */\n%s\n\n", css.Name, css.Scope, css.Content); err != nil {
			return err
		}
	}
	if len(ctx.Images) > 0 {
		if _, err := fmt.Fprintln(f.Out, "Images:"); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SCOPE\tNAME\tID")
		for _, img := range ctx.Images {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", img.Scope, img.Name, img.ID)
		}
		return tw.Flush()
	}
	return nil
}
