package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var contextIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validContextID(id string) bool {
	return contextIDPattern.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func newKnowledgeContextPackCmd(f *Factory) *cobra.Command {
	var depth int
	var include, agent string
	cmd := &cobra.Command{
		Use:   "context-pack <feature-ref>",
		Short: "Load relevant feature context and a separate related-item index (UUID or KEY-N)",
		Long: "Load placement, build, constraints and lineage with provenance and excerpts. " +
			"JSON is the default. This supplements feature, security and design context; it does not replace them. " +
			"Read layers first; related_layers contains additional relationships, not automatic dependencies. " +
			"Inspect each item's relevance, reason and directed path. Older backends without related_layers do not provide this separation. " +
			"Do not bulk-fetch either group: choose real items needed for a specific task question, then use knowledge get <item-id> --json. " +
			"Inspect truncated and use targeted knowledge search --query <question> --json when needed. " +
			"A file URI may only be a reference: read the actual file from the authorized checkout. " +
			"With --agent, record an authenticated retrieval receipt and retain load_id for knowledge get --context-load. " +
			"The agent label is client-declared; receipts prove retrieval, not model comprehension. Without --agent no receipt is recorded.",
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
			method := http.MethodGet
			var body []byte
			if cmd.Flags().Changed("agent") {
				switch agent {
				case "codex", "claude-code", "copilot", "kiro", "antigravity", "gemini-cli", "cli":
				default:
					return fmt.Errorf("--agent must name codex, claude-code, copilot, kiro, antigravity, gemini-cli or cli")
				}
				method = http.MethodPost
				path = "/api/knowledge/features/" + url.PathEscape(ref) + "/context-loads"
				body, _ = json.Marshal(map[string]string{"agent": agent})
			}
			if len(query) > 0 {
				path += "?" + query.Encode()
			}
			raw, err := f.request(cmd.Context(), method, path, body)
			if err != nil {
				return err
			}
			if method == http.MethodPost {
				var receipt struct {
					LoadID   string    `json:"load_id"`
					LoadedAt time.Time `json:"loaded_at"`
				}
				if err := json.Unmarshal(raw, &receipt); err != nil || !validContextID(receipt.LoadID) || receipt.LoadedAt.IsZero() {
					return fmt.Errorf("server did not return a valid context retrieval receipt; no tracked package can be confirmed")
				}
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 0, "maximum relationship steps (1-5; server default 2)")
	cmd.Flags().StringVar(&include, "include", "", "comma-separated layers; omitted means all layers plus passages")
	cmd.Flags().StringVar(&agent, "agent", "", "declare the calling agent and record this retrieval in the feature context board")
	return jsonByDefault(cmd)
}
