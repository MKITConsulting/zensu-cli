package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const wikiPageLimit = 50

type wikiPageItem struct {
	ID        string `json:"id"`
	ProductID string `json:"productId"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	Audience  string `json:"audience"`
	ParentID  string `json:"parentId"`
}

func NewWikiCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "wiki",
		Aliases: []string{"wiki-pages"},
		Short:   "Manage wiki pages",
	}
	cmd.AddCommand(
		newWikiListCmd(f),
		newWikiCreateCmd(f),
		newWikiUpdateCmd(f),
	)
	return cmd
}

func newWikiListCmd(f *Factory) *cobra.Command {
	var product, audience, parent string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List a product's wiki pages",
		Long:         "List a product's wiki pages, optionally filtered by audience or parent page.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			q := url.Values{}
			q.Set("productId", product)
			if audience != "" {
				q.Set("audience", audience)
			}
			if parent != "" {
				q.Set("parentPageId", parent)
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/wiki/pages?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var pages []wikiPageItem
			decoded := json.Unmarshal(raw, &pages) == nil
			if asJSON || !decoded {
				err = printJSON(f.Out, raw)
			} else {
				tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tSLUG\tTITLE\tAUDIENCE")
				for _, p := range pages {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Slug, p.Title, p.Audience)
				}
				err = tw.Flush()
			}
			if err != nil || !decoded || len(pages) != wikiPageLimit {
				return err
			}
			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: the server returns at most %d wiki pages per query, so pages after the first %d may be missing; narrow the query with --parent or --audience\n", wikiPageLimit, wikiPageLimit)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (required)")
	cmd.Flags().StringVar(&audience, "audience", "", "filter by audience (end_user|developer|admin|internal)")
	cmd.Flags().StringVar(&parent, "parent", "", "filter by parent page UUID to get child pages")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWikiCreateCmd(f *Factory) *cobra.Command {
	var product, title, content, entityType, entityID, docType, audience, visibility string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a wiki page for a product",
		Long:         "Create a new wiki page for a product. Wiki pages store documentation content such as user-facing guides, API references, internal docs, and release notes.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" || title == "" || content == "" {
				return fmt.Errorf("--product, --title and --content are required")
			}
			payload := map[string]string{
				"productId": product,
				"title":     title,
				"content":   content,
			}
			if entityType != "" {
				payload["entityType"] = entityType
			}
			if entityID != "" {
				payload["entityId"] = entityID
			}
			if docType != "" {
				payload["docType"] = docType
			}
			if audience != "" {
				payload["audience"] = audience
			}
			if visibility != "" {
				payload["visibility"] = visibility
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/wiki/pages", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var p wikiPageItem
			_ = json.Unmarshal(raw, &p)
			_, err = fmt.Fprintf(f.Out, "Created wiki page %s %q (%s)\n", p.Slug, p.Title, p.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (required)")
	cmd.Flags().StringVar(&title, "title", "", "page title (required)")
	cmd.Flags().StringVar(&content, "content", "", "page content in markdown (required)")
	cmd.Flags().StringVar(&entityType, "entity-type", "", "entity type to link (feature|component|product)")
	cmd.Flags().StringVar(&entityID, "entity-id", "", "entity UUID to link the page to")
	cmd.Flags().StringVar(&docType, "doc-type", "", "document type (user_facing|api_reference|tutorial|adr|release_notes|internal|migration_guide|overview)")
	cmd.Flags().StringVar(&audience, "audience", "", "target audience (end_user|developer|admin|internal)")
	cmd.Flags().StringVar(&visibility, "visibility", "", "page visibility: public|private (defaults to private)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWikiUpdateCmd(f *Factory) *cobra.Command {
	var title, content, changeSummary, visibility string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "update <page-id>",
		Short:        "Update an existing wiki page",
		Long:         "Update an existing wiki page. Supports partial updates — only provided fields are changed.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("content") &&
				!cmd.Flags().Changed("change-summary") && !cmd.Flags().Changed("visibility") {
				return fmt.Errorf("nothing to update: pass --title, --content, --change-summary, or --visibility")
			}
			payload := map[string]string{}
			if cmd.Flags().Changed("title") {
				payload["title"] = title
			}
			if cmd.Flags().Changed("content") {
				payload["content"] = content
			}
			if cmd.Flags().Changed("change-summary") {
				payload["changeSummary"] = changeSummary
			}
			if cmd.Flags().Changed("visibility") {
				payload["visibility"] = visibility
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPut, "/api/wiki/pages/"+args[0], body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Updated wiki page %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new page title")
	cmd.Flags().StringVar(&content, "content", "", "new page content in markdown")
	cmd.Flags().StringVar(&changeSummary, "change-summary", "", "summary of changes made")
	cmd.Flags().StringVar(&visibility, "visibility", "", "page visibility: public|private")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
