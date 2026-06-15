package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewMetaCmd groups Zensu agent-integration / workflow helper commands.
//
// All three underlying MCP tools (scaffold_agent, suggest_workflow,
// get_workflow_guide) are computed inside the MCP server itself: scaffold_agent
// and get_workflow_guide render local templates / a static guide map, and
// suggest_workflow composes its recommendation in-process from several read
// endpoints (features, journeys, ghost scans, security posture, visions) — there
// is no single REST endpoint for any of them. The CLI is a thin REST client, so
// these verbs have no confirmable path to call and are exposed as stubs that
// explain where the behavior lives rather than inventing an endpoint.
func NewMetaCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Agent integration and workflow guidance helpers",
	}
	cmd.AddCommand(
		newMetaScaffoldAgentCmd(f),
		newMetaSuggestWorkflowCmd(f),
		newMetaWorkflowGuideCmd(f),
	)
	return cmd
}

func newMetaScaffoldAgentCmd(_ *Factory) *cobra.Command {
	var cli string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "scaffold-agent",
		Short:        "Generate CLI-specific Zensu adapter files",
		Long:         "Generate CLI-specific adapter files for integrating Zensu with different AI coding tools. Supports Claude Code, Kiro, Cursor, and Copilot. For Claude Code, the zensu-claude-code plugin is the recommended approach.",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("scaffold-agent is not available over REST: adapter files are generated locally by the MCP server (scaffold_agent tool); use the Zensu MCP server or the zensu-claude-code plugin instead")
		},
	}
	cmd.Flags().StringVar(&cli, "cli", "", "target CLI: claude-code|kiro|cursor|copilot|all (default: all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newMetaSuggestWorkflowCmd(_ *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "suggest-workflow",
		Short:        "Suggest next workflow actions for a product",
		Long:         "Analyze product state and suggest next workflow actions. Returns proactive recommendations based on missing security reviews, unlinked tests, empty journeys, and other gaps.",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			return fmt.Errorf("suggest-workflow is not available over REST: the recommendation is composed in the MCP server (suggest_workflow tool) from several read endpoints (features, journeys, ghost scans, security posture, visions); use the Zensu MCP server instead")
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newMetaWorkflowGuideCmd(_ *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "workflow-guide <workflow>",
		Short:        "Get a structured workflow guide",
		Long:         "Get a structured workflow guide as JSON. Useful for clients without MCP prompt support. Returns step-by-step instructions with tool references. Workflow name: bootstrap|security-review|implement|pulse|ghost-scan",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return fmt.Errorf("workflow-guide is not available over REST: the guide for %q is served from a static map inside the MCP server (get_workflow_guide tool); use the Zensu MCP server instead", args[0])
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
