package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newKnowledgeContextPackCmd(f *Factory) *cobra.Command {
	var depth int
	var include string
	cmd := &cobra.Command{
		Use:   "context-pack <feature-ref>",
		Short: "Load the linked knowledge around a feature (UUID or KEY-N)",
		Long: "Load placement, build, constraints and lineage with provenance and excerpts. " +
			"JSON is the default. This supplements feature, security and design context; it does not replace them. " +
			"Inspect truncated, then use knowledge get <item-id> --json for relevant real items. " +
			"A file URI may only be a reference: read the actual file from the authorized checkout. " +
			"This read never syncs a product or records agent usage.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if strings.TrimSpace(ref) == "" {
				return fmt.Errorf("feature reference is required")
			}
			query := url.Values{}
			if cmd.Flags().Changed("depth") {
				if depth < 1 || depth > 5 {
					return fmt.Errorf("--depth must be between 1 and 5")
				}
				query.Set("depth", strconv.Itoa(depth))
			}
			if cmd.Flags().Changed("include") {
				layers := strings.Split(include, ",")
				for i, layer := range layers {
					layers[i] = strings.TrimSpace(layer)
					switch layers[i] {
					case "placement", "build", "constraints", "lineage", "passages":
					default:
						return fmt.Errorf("invalid --include value %q: use placement,build,constraints,lineage,passages", layer)
					}
				}
				query.Set("include", strings.Join(layers, ","))
			}
			path := "/api/knowledge/features/" + url.PathEscape(ref) + "/context-pack"
			if len(query) > 0 {
				path += "?" + query.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 0, "maximum relationship steps (1-5; server default 2)")
	cmd.Flags().StringVar(&include, "include", "", "comma-separated layers; omitted means all layers plus passages")
	return jsonByDefault(cmd)
}
