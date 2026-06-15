package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type journeyItem struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	JourneyType *string `json:"journey_type"`
	Priority    *string `json:"priority"`
	Status      *string `json:"status"`
}

type journeyStepItem struct {
	ID              string  `json:"id"`
	StepOrder       int     `json:"step_order"`
	Title           string  `json:"title"`
	InteractionType *string `json:"interaction_type"`
}

func NewJourneysCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "journeys",
		Aliases: []string{"journey"},
		Short:   "Manage user journeys",
	}
	cmd.AddCommand(
		newJourneysListCmd(f),
		newJourneysGetCmd(f),
		newJourneysCreateCmd(f),
		newJourneysStepCmd(f),
		newJourneysStepsCmd(f),
		newJourneysHealthCmd(f),
		newJourneysSuggestCmd(f),
	)
	return cmd
}

func newJourneysListCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List all user journeys for a product",
		Long:         "List all user journeys for a product. Returns journey metadata including type, priority, status and persona.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/journeys", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []journeyItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tTITLE\tTYPE\tPRIORITY\tSTATUS")
			for _, j := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.Slug, j.Title, journeyStr(j.JourneyType), journeyStr(j.Priority), journeyStr(j.Status))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newJourneysGetCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "get <journey-id>",
		Short:        "Get a specific user journey by ID",
		Long:         "Get a specific user journey by ID. Returns full journey details including type, priority, status and persona.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/journeys/"+args[0], nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func newJourneysCreateCmd(f *Factory) *cobra.Command {
	var product, title, slug, description, journeyType, priority, persona, tier string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a user journey for a product",
		Long:         "Create a user journey for a product. Journeys represent critical user paths through features and are used for release gate validation.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			s := slug
			if s == "" {
				s = slugify(title)
			}
			if s == "" {
				return fmt.Errorf("could not derive a slug from --title; pass --slug explicitly")
			}
			payload := map[string]string{
				"title": title,
				"slug":  s,
			}
			if description != "" {
				payload["description"] = description
			}
			if journeyType != "" {
				payload["journeyType"] = journeyType
			}
			if priority != "" {
				payload["priority"] = priority
			}
			if persona != "" {
				payload["persona"] = persona
			}
			if tier != "" {
				payload["tierId"] = tier
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/journeys", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var j journeyItem
			_ = json.Unmarshal(raw, &j)
			_, err = fmt.Fprintf(f.Out, "Created journey %s %s (%s)\n", j.Slug, j.Title, j.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "journey title (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe identifier (derived from --title if omitted)")
	cmd.Flags().StringVar(&description, "description", "", "journey description")
	cmd.Flags().StringVar(&journeyType, "type", "", "type: critical|happy_path|edge_case|error_path|onboarding")
	cmd.Flags().StringVar(&priority, "priority", "", "priority level: critical|high|medium|low")
	cmd.Flags().StringVar(&persona, "persona", "", "target user persona")
	cmd.Flags().StringVar(&tier, "tier", "", "tier UUID this journey applies to")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newJourneysStepCmd(f *Factory) *cobra.Command {
	var product, journey, title, feature, description, interactionType, expectedResult string
	var stepOrder int
	var isCritical, asJSON bool
	cmd := &cobra.Command{
		Use:          "step <journey-id>",
		Short:        "Add a step to a user journey",
		Long:         "Add a step to a user journey. Steps represent individual user interactions that make up a journey. Link steps to features via --feature to enable journey health tracking.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			if !cmd.Flags().Changed("step-order") {
				return fmt.Errorf("--step-order is required")
			}
			journey = args[0]
			payload := map[string]any{
				"title":     title,
				"stepOrder": stepOrder,
			}
			if feature != "" {
				payload["featureId"] = feature
			}
			if description != "" {
				payload["description"] = description
			}
			if interactionType != "" {
				payload["interactionType"] = interactionType
			}
			if expectedResult != "" {
				payload["expectedResult"] = expectedResult
			}
			if cmd.Flags().Changed("critical") {
				payload["isCritical"] = isCritical
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/products/"+product+"/journeys/"+journey+"/steps", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var st journeyStepItem
			_ = json.Unmarshal(raw, &st)
			_, err = fmt.Fprintf(f.Out, "Added step %d %q to journey %s\n", st.StepOrder, st.Title, journey)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "step title (required)")
	cmd.Flags().IntVar(&stepOrder, "step-order", 0, "1-based step order within the journey (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "feature UUID this step is linked to")
	cmd.Flags().StringVar(&description, "description", "", "step description")
	cmd.Flags().StringVar(&interactionType, "interaction-type", "", "interaction type: action|navigation|input|validation|output|wait")
	cmd.Flags().StringVar(&expectedResult, "expected-result", "", "expected outcome of this step")
	cmd.Flags().BoolVar(&isCritical, "critical", false, "whether this step is critical to the journey")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newJourneysStepsCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "steps <journey-id>",
		Short:        "List all steps of a user journey",
		Long:         "List all steps of a user journey. Returns steps ordered by step_order with their linked features and interaction types.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/journeys/"+args[0]+"/steps", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []journeyStepItem `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ORDER\tTITLE\tINTERACTION")
			for _, st := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", strconv.Itoa(st.StepOrder), st.Title, journeyStr(st.InteractionType))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newJourneysHealthCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "health <journey-id>",
		Short:        "Analyze the health of a specific user journey",
		Long:         "Analyze the health of a specific user journey. Returns a health score, status, weakest link feature and per-step health results.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/journeys/"+args[0]+"/health", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func newJourneysSuggestCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "suggest",
		Short:        "Get aggregated product context to help suggest user journeys",
		Long:         "Get aggregated product context to help suggest user journeys. Returns product info, tiers, features, components, existing journeys, and source files linked to features (including ghost scan discoveries). ghostScanCount indicates whether a codebase scan has been performed.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+product+"/journeys/context", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

func journeyStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
