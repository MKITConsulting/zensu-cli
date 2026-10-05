package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var journeyStepMutableFlags = []string{"title", "step-order", "feature", "description", "interaction-type", "expected-result", "critical"}

type journeyItem struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	JourneyType *string `json:"journey_type"`
	Priority    *string `json:"priority"`
	Status      *string `json:"status"`
}

type journeyStepFull struct {
	ID              string  `json:"id"`
	FeatureID       *string `json:"feature_id"`
	StepOrder       int     `json:"step_order"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	InteractionType *string `json:"interaction_type"`
	ExpectedResult  *string `json:"expected_result"`
	IsCritical      *bool   `json:"is_critical"`
}

func journeyStepsPath(product, journey string) (string, error) {
	for _, segment := range []struct{ name, value string }{{"--product", product}, {"<journey-id>", journey}} {
		if err := validatePathSegment(segment.name, segment.value); err != nil {
			return "", err
		}
	}
	return "/api/products/" + url.PathEscape(product) + "/journeys/" + url.PathEscape(journey) + "/steps", nil
}

func journeyStepPath(product, journey, step string) (string, error) {
	base, err := journeyStepsPath(product, journey)
	if err != nil {
		return "", err
	}
	if err := validatePathSegment("<step-id>", step); err != nil {
		return "", err
	}
	return base + "/" + url.PathEscape(step), nil
}

func validatePathSegment(name, value string) error {
	switch value {
	case "":
		return fmt.Errorf("%s must not be empty", name)
	case ".", "..":
		return fmt.Errorf("%s must not be %q", name, value)
	}
	return nil
}

type journeyStepReplacement struct {
	Title           string  `json:"title"`
	StepOrder       int     `json:"stepOrder"`
	FeatureID       *string `json:"featureId"`
	Description     *string `json:"description"`
	InteractionType *string `json:"interactionType"`
	ExpectedResult  *string `json:"expectedResult"`
	IsCritical      *bool   `json:"isCritical"`
}

func replacementFrom(s *journeyStepFull) journeyStepReplacement {
	return journeyStepReplacement{
		Title:           s.Title,
		StepOrder:       s.StepOrder,
		FeatureID:       s.FeatureID,
		Description:     s.Description,
		InteractionType: s.InteractionType,
		ExpectedResult:  s.ExpectedResult,
		IsCritical:      s.IsCritical,
	}
}

func displayOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var journeyStepRequiredResponseKeys = []string{"id", "feature_id", "step_order", "title", "description", "interaction_type", "expected_result", "is_critical"}

var journeyStepNonNullKeys = map[string]bool{"id": true, "step_order": true, "title": true}

var journeyStepKnownResponseKeys = map[string]bool{
	"id": true, "journey_id": true, "feature_id": true, "step_order": true,
	"title": true, "description": true, "interaction_type": true,
	"expected_result": true, "is_critical": true, "created_at": true,
	"organization_id": true, "total_count": true,
}

func prefixedStepFlagNames() []string {
	names := make([]string, 0, len(journeyStepMutableFlags))
	for _, name := range journeyStepMutableFlags {
		names = append(names, "--"+name)
	}
	return names
}

func loadJourneyStep(ctx context.Context, f *Factory, product, journey, step string) (*journeyStepFull, error) {
	listPath, err := journeyStepsPath(product, journey)
	if err != nil {
		return nil, err
	}
	raw, err := f.listAll(ctx, listPath, nil, "journey steps")
	if err != nil {
		return nil, err
	}
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("could not decode the journey's steps: %w", err)
	}
	for _, element := range env.Data {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(element, &probe); err != nil {
			return nil, fmt.Errorf("could not decode a step of journey %s: %w", journey, err)
		}
		var parsed journeyStepFull
		if err := json.Unmarshal(element, &parsed); err != nil {
			return nil, fmt.Errorf("could not decode a step of journey %s: %w", journey, err)
		}
		if !strings.EqualFold(parsed.ID, step) {
			continue
		}
		for _, key := range journeyStepRequiredResponseKeys {
			value, present := probe[key]
			if !present {
				return nil, fmt.Errorf("the server response for step %s is missing %q; refusing to send a replacement that would clear it", step, key)
			}
			if journeyStepNonNullKeys[key] && string(value) == "null" {
				return nil, fmt.Errorf("the server returned a null %q for step %s; refusing to send a replacement built from it", step, key)
			}
		}
		for key := range probe {
			if !journeyStepKnownResponseKeys[key] {
				return nil, fmt.Errorf("the server returned an unknown field %q for step %s; refusing to send a replacement that would clear it — upgrade the CLI", key, step)
			}
		}
		return &parsed, nil
	}
	return nil, fmt.Errorf("step %s not found among the %d steps returned for journey %s", step, len(env.Data), journey)
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
		newJourneysStepUpdateCmd(f),
		newJourneysStepDeleteCmd(f),
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
			raw, err := f.listAll(cmd.Context(), "/api/products/"+product+"/journeys", nil, "journeys")
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
			createPath, err := journeyStepsPath(product, journey)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, createPath, body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var st journeyStepFull
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

func newJourneysStepUpdateCmd(f *Factory) *cobra.Command {
	var product, title, description, feature, interactionType, expectedResult string
	var stepOrder int
	var isCritical, asJSON bool
	cmd := &cobra.Command{
		Use:   "step-update <journey-id> <step-id>",
		Short: "Update an existing step of a user journey",
		Long: "Update an existing step of a user journey.\n\n" +
			"The server replaces the whole step row, so this command first reads the step, " +
			"applies the flags you passed on top of it, and writes the complete record back. " +
			"Fields you leave out are resent unchanged rather than preserved by the server.\n\n" +
			"There is no optimistic locking: a concurrent edit made between the read and the " +
			"write is overwritten (last writer wins).",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			touched := false
			for _, name := range journeyStepMutableFlags {
				if cmd.Flags().Changed(name) {
					touched = true
					break
				}
			}
			if !touched {
				return fmt.Errorf("pass at least one of %s", strings.Join(prefixedStepFlagNames(), ", "))
			}
			if cmd.Flags().Changed("step-order") {
				if stepOrder < 1 {
					return fmt.Errorf("--step-order must be 1 or greater")
				}
				if stepOrder > math.MaxInt32 {
					return fmt.Errorf("--step-order %d exceeds the maximum the server accepts", stepOrder)
				}
			}
			if cmd.Flags().Changed("title") && title == "" {
				return fmt.Errorf("--title must not be empty; the server rejects a step without a title")
			}
			stepPath, err := journeyStepPath(product, args[0], args[1])
			if err != nil {
				return err
			}

			current, err := loadJourneyStep(cmd.Context(), f, product, args[0], args[1])
			if err != nil {
				return err
			}

			payload := replacementFrom(current)
			if cmd.Flags().Changed("title") {
				payload.Title = title
			}
			if cmd.Flags().Changed("step-order") {
				payload.StepOrder = stepOrder
			}
			if cmd.Flags().Changed("feature") {
				payload.FeatureID = nilIfEmpty(feature)
			}
			if cmd.Flags().Changed("description") {
				payload.Description = nilIfEmpty(description)
			}
			if cmd.Flags().Changed("interaction-type") {
				payload.InteractionType = nilIfEmpty(interactionType)
			}
			if cmd.Flags().Changed("expected-result") {
				payload.ExpectedResult = nilIfEmpty(expectedResult)
			}
			if cmd.Flags().Changed("critical") {
				payload.IsCritical = &isCritical
			}

			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPut, stepPath, body)
			if err != nil {
				return err
			}
			if len(raw) == 0 {
				if asJSON {
					return printJSON(f.Out, []byte("{}"))
				}
				_, err = fmt.Fprintf(f.Out, "Updated step %d %q in journey %s (feature %s, critical %t); the server returned no body\n",
					payload.StepOrder, payload.Title, args[0], displayOr(payload.FeatureID, "none"), payload.IsCritical != nil && *payload.IsCritical)
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var st journeyStepFull
			if err := json.Unmarshal(raw, &st); err != nil {
				return fmt.Errorf("step updated, but the response could not be decoded: %w", err)
			}
			if !strings.EqualFold(st.ID, args[1]) {
				return fmt.Errorf("step updated, but the server returned an unexpected record (id %q); re-read the step to confirm its state", st.ID)
			}
			_, err = fmt.Fprintf(f.Out, "Updated step %d %q in journey %s (feature %s, critical %t)\n",
				st.StepOrder, st.Title, args[0], displayOr(st.FeatureID, "none"), st.IsCritical != nil && *st.IsCritical)
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "step title")
	cmd.Flags().IntVar(&stepOrder, "step-order", 0, "1-based step order within the journey")
	cmd.Flags().StringVar(&feature, "feature", "", `feature UUID this step is linked to; pass "" to unlink`)
	cmd.Flags().StringVar(&description, "description", "", `step description; pass "" to clear`)
	cmd.Flags().StringVar(&interactionType, "interaction-type", "", `interaction type: action|navigation|input|validation|output|wait; pass "" to clear`)
	cmd.Flags().StringVar(&expectedResult, "expected-result", "", `expected outcome of this step; pass "" to clear`)
	cmd.Flags().BoolVar(&isCritical, "critical", false, "whether this step is critical to the journey; use --critical=false to clear it")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newJourneysStepDeleteCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "step-delete <journey-id> <step-id>",
		Short:        "Delete a step from a user journey",
		Long:         "Delete a step from a user journey. The remaining steps keep their step order, so reorder them afterwards if the sequence must stay gap-free.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if product == "" {
				return fmt.Errorf("--product is required")
			}
			stepPath, err := journeyStepPath(product, args[0], args[1])
			if err != nil {
				return err
			}
			if _, err := f.request(cmd.Context(), http.MethodDelete, stepPath, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Out, "Deleted step %s from journey %s\n", args[1], args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
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
			raw, err := f.listAll(cmd.Context(), "/api/products/"+product+"/journeys/"+args[0]+"/steps", nil, "journey steps")
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []journeyStepFull `json:"data"`
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
