package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	cmd.AddCommand(newProductsListCmd(f), newProductsGetCmd(f), newProductsCreateCmd(f))
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
