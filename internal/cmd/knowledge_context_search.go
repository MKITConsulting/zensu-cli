package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

type contextCandidate struct {
	ItemID    string `json:"item_id"`
	FeatureID string `json:"feature_id"`
	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	Excerpt   string `json:"excerpt"`
}

// Discovery intentionally does not choose by retrieval score: scores are ranks,
// not calibrated confidence. The calling agent resolves task and repository scope.
func newKnowledgeContextSearchCmd(f *Factory) *cobra.Command {
	var query, product string
	cmd := &cobra.Command{
		Use: "context-search", Short: "Find feature candidates for a task before loading tracked context",
		Long: "Search a task description and return up to five distinct feature candidates with canonical feature and product IDs. " +
			"This is bounded discovery, not a confirmed selection or a context-load receipt. Match the task and repository/product scope; " +
			"when one candidate fits, load context-pack <feature-id> --agent <host>, then read sources with --context-load. " +
			"Ask when candidates remain ambiguous. --product filters the bounded search results; an empty result is not proof that a product has no matching feature.",
		Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query = strings.TrimSpace(query)
			if query == "" || utf8.RuneCountInString(query) > 2000 {
				return fmt.Errorf("--query must contain between 1 and 2000 characters")
			}
			if cmd.Flags().Changed("product") && !validContextID(product) {
				return fmt.Errorf("--product must be a non-zero product UUID")
			}
			body, _ := json.Marshal(map[string]any{"query": query, "limit": 10, "scope": "org"})
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/knowledge/search", body)
			if err != nil {
				return err
			}
			var hits struct {
				Results []knowledgeSearchResult `json:"results"`
			}
			if err := json.Unmarshal(raw, &hits); err != nil {
				return err
			}
			if hits.Results == nil {
				return fmt.Errorf("server did not return search results")
			}
			candidates := []contextCandidate{}
			seenItems, seenFeatures := map[string]bool{}, map[string]bool{}
			inspected := 0
			for _, hit := range hits.Results {
				if hit.SourceType != "feature" || !validContextID(hit.ItemID) || seenItems[hit.ItemID] {
					continue
				}
				if inspected == 5 {
					break
				}
				seenItems[hit.ItemID] = true
				inspected++
				raw, err := f.request(cmd.Context(), http.MethodGet, "/api/knowledge/items/"+url.PathEscape(hit.ItemID), nil)
				if err != nil {
					return err
				}
				var item struct {
					ID        string `json:"id"`
					FeatureID string `json:"feature_id"`
					ProductID string `json:"product_id"`
					Title     string `json:"title"`
					Content   string `json:"content"`
				}
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if item.ID != hit.ItemID || !validContextID(item.FeatureID) || !validContextID(item.ProductID) {
					return fmt.Errorf("server returned an invalid feature identity")
				}
				if seenFeatures[item.FeatureID] || (product != "" && item.ProductID != product) {
					continue
				}
				seenFeatures[item.FeatureID] = true
				candidates = append(candidates, contextCandidate{ItemID: item.ID, FeatureID: item.FeatureID, ProductID: item.ProductID, Title: contextText(item.Title, 200), Excerpt: contextText(item.Content, 1200)})
			}
			data, err := json.Marshal(struct {
				Candidates        []contextCandidate `json:"candidates"`
				SelectionRequired bool               `json:"selection_required"`
				Bounded           bool               `json:"bounded"`
			}{candidates, true, true})
			if err != nil {
				return err
			}
			return printJSON(f.Out, data)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "task description to search (required; sent to the configured Zensu backend)")
	cmd.Flags().StringVar(&product, "product", "", "restrict candidates to this product UUID within the bounded search results")
	return jsonByDefault(cmd)
}

func contextText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}
