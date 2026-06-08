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
