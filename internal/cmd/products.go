package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type productItem struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	ProductType *string `json:"product_type"`
}

func NewProductsCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "products",
		Aliases: []string{"product"},
		Short:   "Manage products",
	}
	cmd.AddCommand(
		newProductsListCmd(f),
		newProductsGetCmd(f),
		newProductsCreateCmd(f),
		newProductsVisionCreateCmd(f),
		newProductsVisionGetCmd(f),
		newProductsBootstrapApplyCmd(f),
		newProductsBootstrapStepCmd(f),
		newProductsImportCmd(f),
	)
	return cmd
}

func newProductsListCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List products",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []productItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tTYPE")
			for _, p := range env.Data {
				ptype := ""
				if p.ProductType != nil {
					ptype = *p.ProductType
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.ID, p.Name, ptype)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newProductsGetCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "get <product-id>",
		Short:        "Get a product by ID",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func normalizeProductType(t string) string {
	n := strings.ToLower(strings.TrimSpace(t))
	switch n {
	case "public":
		return "public_product"
	case "internal":
		return "internal_product"
	default:
		return n
	}
}

func newProductsCreateCmd(f *Factory) *cobra.Command {
	var name, productType, slug, description string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			payload := map[string]string{"name": name}
			if productType != "" {
				payload["productType"] = normalizeProductType(productType)
			}
			if slug != "" {
				payload["slug"] = slug
			}
			if description != "" {
				payload["description"] = description
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var p productItem
			_ = json.Unmarshal(raw, &p)
			_, err = fmt.Fprintf(f.Out, "Created product %s (%s)\n", p.Name, p.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "product name (required)")
	cmd.Flags().StringVar(&productType, "type", "", "product type (public|internal|hybrid)")
	cmd.Flags().StringVar(&slug, "slug", "", "product slug")
	cmd.Flags().StringVar(&description, "description", "", "product description")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

type visionItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func newProductsVisionCreateCmd(f *Factory) *cobra.Command {
	var product, title, content, source string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "vision-create",
		Short: "Create a product vision",
		Long: "Create a new product vision document. Visions capture product ideas and " +
			"can be bootstrapped into components and features. Include the architecture type " +
			"(e.g. microservices, monolith, CLI, mobile app, API-only, library/SDK) and target " +
			"audience in the vision content to enable better decomposition during bootstrap.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			if content == "" {
				return fmt.Errorf("--content is required")
			}
			payload := map[string]string{"title": title, "content": content}
			if product != "" {
				payload["productId"] = product
			}
			if source != "" {
				payload["source"] = source
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/visions", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var v visionItem
			_ = json.Unmarshal(raw, &v)
			_, err = fmt.Fprintf(f.Out, "Created vision %s (%s)\n", v.Title, v.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (optional — omit for greenfield visions)")
	cmd.Flags().StringVar(&title, "title", "", "vision title (required)")
	cmd.Flags().StringVar(&content, "content", "", "vision content, markdown or plain text (required)")
	cmd.Flags().StringVar(&source, "source", "", "vision source: studio|import|claude-code")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newProductsVisionGetCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "vision-get <vision-id>",
		Short:        "Get a product vision by ID",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/visions/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

type bootstrapResult struct {
	ComponentsCreated  int `json:"componentsCreated"`
	FeaturesCreated    int `json:"featuresCreated"`
	SubfeaturesCreated int `json:"subfeaturesCreated"`
}

func newProductsBootstrapApplyCmd(f *Factory) *cobra.Command {
	var result string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "bootstrap-apply <vision-id>",
		Short: "Apply a bootstrap result to a vision",
		Long: "Apply bootstrap result from vision analysis. Pass the structured components " +
			"and features as JSON via --result. Schema: " +
			`{"components":[{"name":"...","slug":"...","description":"..."}],"features":[{"title":"...","slug":"...","description":"...","component":"<component-slug>","priority":"critical|high|medium|low","estimatedEffort":"S|M|L|XL","securityClassification":"public|internal|confidential|restricted","securityReasoning":"...","featureScope":"public_facing|internal_only","subfeatures":[...]}]}. ` +
			"The component field must reference a slug from the components array.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if result == "" {
				return fmt.Errorf("--result is required")
			}
			var parsed struct {
				Components []json.RawMessage `json:"components"`
				Features   []json.RawMessage `json:"features"`
			}
			if err := json.Unmarshal([]byte(result), &parsed); err != nil {
				return fmt.Errorf("invalid --result JSON: %w", err)
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/visions/"+args[0]+"/bootstrap/apply", []byte(result))
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var br bootstrapResult
			_ = json.Unmarshal(raw, &br)
			_, err = fmt.Fprintf(f.Out, "Bootstrapped vision %s: %d components, %d features, %d subfeatures\n",
				args[0], br.ComponentsCreated, br.FeaturesCreated, br.SubfeaturesCreated)
			return err
		},
	}
	cmd.Flags().StringVar(&result, "result", "", "bootstrap result JSON with components and features (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newProductsBootstrapStepCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "bootstrap-step <vision-id> <step>",
		Short: "Mark a post-bootstrap step as completed",
		Long: "Set a post-bootstrap step as completed. The step is the absolute step number " +
			"you just finished (not an increment). Steps: 1=Review Features, 2=User Journeys, " +
			"3=Security Setup, 4=Tier Availability (optional — skip by setting 5 directly after 3), " +
			"5=Generate CLAUDE.md. The backend enforces sequential order — you can only set " +
			"current_step + 1 (or +2 to skip optional step 4).",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			step, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("step must be a number between 1 and 5")
			}
			body, err := json.Marshal(map[string]int{"step": step})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPatch, "/api/visions/"+args[0]+"/bootstrap/step", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Vision %s bootstrap step → %d\n", args[0], step)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

type repoImportItem struct {
	ID      string `json:"id"`
	RepoURL string `json:"repo_url"`
}

func newProductsImportCmd(f *Factory) *cobra.Command {
	var repoURL, repoType string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "import <product-id>",
		Short:        "Import a repository into a product",
		Long:         "Import a repository into a product.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repoURL == "" {
				return fmt.Errorf("--repo-url is required")
			}
			payload := map[string]string{"repoUrl": repoURL}
			if repoType != "" {
				payload["repoType"] = repoType
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+args[0]+"/repo-import", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var ri repoImportItem
			_ = json.Unmarshal(raw, &ri)
			_, err = fmt.Fprintf(f.Out, "Imported repo %s (%s)\n", ri.RepoURL, ri.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&repoURL, "repo-url", "", "repository URL (required)")
	cmd.Flags().StringVar(&repoType, "repo-type", "", "repository type: github|gitlab|bitbucket|local")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
