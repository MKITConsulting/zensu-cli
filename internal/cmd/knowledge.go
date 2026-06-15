package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type knowledgeSearchResult struct {
	ItemID     string  `json:"itemId"`
	SourceType string  `json:"sourceType"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
}

type knowledgeSource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SourceType string `json:"source_type"`
	SyncStatus string `json:"sync_status"`
}

func NewKnowledgeCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "knowledge",
		Aliases: []string{"kb"},
		Short:   "Search and inspect the organization's knowledge pool",
	}
	cmd.AddCommand(
		newKnowledgeSearchCmd(f),
		newKnowledgeGetCmd(f),
		newKnowledgeSourcesCmd(f),
	)
	return cmd
}

func newKnowledgeSearchCmd(f *Factory) *cobra.Command {
	var query, scope string
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search the knowledge pool by meaning and keyword",
		Long: "Search the organization's knowledge pool (features, visions, journeys, and connected sources) " +
			"by meaning and keyword. Returns ranked passages with provenance (source type, title, item id, score) " +
			"so you can ground your reasoning in the org's own context. Retrieval-only: this returns evidence " +
			"passages, it does NOT generate an answer — you synthesize from the returned chunks and cite their provenance.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if query == "" {
				return fmt.Errorf("--query is required")
			}
			payload := map[string]any{"query": query}
			if scope != "" {
				payload["scope"] = scope
			}
			if cmd.Flags().Changed("limit") {
				payload["limit"] = limit
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/knowledge/search", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Results []knowledgeSearchResult `json:"results"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "SCORE\tSOURCE\tTITLE\tITEM")
			for _, r := range env.Results {
				fmt.Fprintf(tw, "%.3f\t%s\t%s\t%s\n", r.Score, r.SourceType, r.Title, r.ItemID)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "natural-language search query (required)")
	cmd.Flags().StringVar(&scope, "scope", "", "search scope (org|personal; default org)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max passages to return (default 10, max 50)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newKnowledgeGetCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <item-id>",
		Short: "Fetch a full knowledge item by id",
		Long: "Fetch a full knowledge item by id, including its complete content, excerpt, trust level, and " +
			"provenance. Use after search when a ranked passage is relevant and you need the whole source record.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/knowledge/items/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newKnowledgeSourcesCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "List the organization's knowledge sources",
		Long: "List the organization's knowledge sources (auto-managed internal sources plus any connected " +
			"external sources), with sync status and type. Use to see what is indexed and available for search.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/knowledge/sources", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var sources []knowledgeSource
			if err := json.Unmarshal(raw, &sources); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTYPE\tSYNC\tID")
			for _, s := range sources {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.SourceType, s.SyncStatus, s.ID)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
