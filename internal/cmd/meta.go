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
	cmd := &cobra.Command{
		Use:   "scaffold-agent",
		Short: "Generate CLI-specific Zensu adapter files (MCP-only)",
		Long: mcpOnlyLong(
			"Generate CLI-specific adapter files for integrating Zensu with different AI coding tools (Claude Code, Kiro, Cursor, Copilot).",
			"adapter files are generated locally by the Zensu MCP server (scaffold_agent tool)",
			"Use the Zensu MCP server, or for Claude Code the zensu-claude-code plugin (recommended).",
		),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return mcpOnlyError(
				"scaffold-agent",
				"adapter files are generated locally by the MCP server (scaffold_agent tool)",
				"use the Zensu MCP server or the zensu-claude-code plugin instead",
			)
		},
	}
	cmd.Flags().StringVar(&cli, "cli", "", "target CLI: claude-code|kiro|cursor|copilot|all (default: all)")
	return cmd
}

func newMetaSuggestWorkflowCmd(_ *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:   "suggest-workflow",
		Short: "Suggest next workflow actions for a product (MCP-only)",
		Long: mcpOnlyLong(
			"Analyze product state and suggest next workflow actions — proactive recommendations based on missing security reviews, unlinked tests, empty journeys, and other gaps.",
			"the recommendation is composed inside the Zensu MCP server (suggest_workflow tool) from several read endpoints (features, journeys, ghost scans, security posture, visions)",
			"Use the Zensu MCP server instead.",
		),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			return mcpOnlyError(
				"suggest-workflow",
				"the recommendation is composed in the MCP server (suggest_workflow tool) from several read endpoints (features, journeys, ghost scans, security posture, visions)",
				"use the Zensu MCP server instead",
			)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (required)")
	return cmd
}

func newMetaWorkflowGuideCmd(_ *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow-guide <workflow>",
		Short: "Get a structured workflow guide (MCP-only)",
		Long: mcpOnlyLong(
			"Get a structured workflow guide (bootstrap|security-review|implement|pulse|ghost-scan) as step-by-step instructions with tool references.",
			"the guide is served from a static map inside the Zensu MCP server (get_workflow_guide tool)",
			"Use the Zensu MCP server instead.",
		),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return mcpOnlyError(
				"workflow-guide",
				fmt.Sprintf("the guide for %q is served from a static map inside the MCP server (get_workflow_guide tool)", args[0]),
				"use the Zensu MCP server instead",
			)
		},
	}
	return cmd
}

func mcpOnlyError(verb, detail, useShort string) error {
	return fmt.Errorf("%s is not available over REST: %s; %s", verb, detail, useShort)
}

func mcpOnlyLong(intro, why, useSuffix string) string {
	return intro + "\n\nNot available over the REST CLI: " + why + ". " + useSuffix
}
