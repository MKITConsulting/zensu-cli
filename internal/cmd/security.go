package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

func NewSecurityCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "security",
		Aliases: []string{"sec"},
		Short:   "Manage feature security classification, tests, reviews and posture",
	}
	cmd.AddCommand(
		newSecurityClassifyCmd(f),
		newSecurityPostureCmd(f),
		newSecurityScoreCmd(f),
		newSecurityAddTestCmd(f),
		newSecurityReviewCmd(f),
		newSecurityAnalyzeCmd(f),
		newSecurityValidateCmd(f),
		newSecuritySuggestTestsCmd(f),
		newSecurityThreatModelCmd(f),
	)
	return cmd
}

func newSecurityClassifyCmd(f *Factory) *cobra.Command {
	var classification, dataSensitivity, authType, threatModelStatus, pentestStatus string
	var authRequired, inputValidation, rateLimited, encryptionAtRest, encryptionInTransit, auditLogged bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "classify <feature-id>",
		Short: "Set the security classification and attributes of a feature",
		Long: "Set the security classification and security attributes of a feature. " +
			"Automatically recalculates the security score. Higher classifications " +
			"(confidential, restricted) enforce stricter requirements for release gates.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			if cmd.Flags().Changed("classification") {
				payload["securityClassification"] = classification
			}
			if cmd.Flags().Changed("data-sensitivity") {
				payload["dataSensitivity"] = dataSensitivity
			}
			if cmd.Flags().Changed("auth-required") {
				payload["authRequired"] = authRequired
			}
			if cmd.Flags().Changed("auth-type") {
				payload["authType"] = authType
			}
			if cmd.Flags().Changed("input-validation") {
				payload["inputValidation"] = inputValidation
			}
			if cmd.Flags().Changed("rate-limited") {
				payload["rateLimited"] = rateLimited
			}
			if cmd.Flags().Changed("encryption-at-rest") {
				payload["encryptionAtRest"] = encryptionAtRest
			}
			if cmd.Flags().Changed("encryption-in-transit") {
				payload["encryptionInTransit"] = encryptionInTransit
			}
			if cmd.Flags().Changed("audit-logged") {
				payload["auditLogged"] = auditLogged
			}
			if cmd.Flags().Changed("threat-model-status") {
				payload["threatModelStatus"] = threatModelStatus
			}
			if cmd.Flags().Changed("pentest-status") {
				payload["pentestStatus"] = pentestStatus
			}
			if len(payload) == 0 {
				return fmt.Errorf("nothing to set: pass at least one of --classification, --data-sensitivity, --auth-required, --auth-type, --input-validation, --rate-limited, --encryption-at-rest, --encryption-in-transit, --audit-logged, --threat-model-status, --pentest-status")
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPut, "/api/features/"+args[0]+"/security", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Updated security classification for feature %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&classification, "classification", "", "security classification (public|internal|confidential|restricted)")
	cmd.Flags().StringVar(&dataSensitivity, "data-sensitivity", "", "data sensitivity (none|pii|financial|health|credentials)")
	cmd.Flags().BoolVar(&authRequired, "auth-required", false, "whether authentication is required")
	cmd.Flags().StringVar(&authType, "auth-type", "", "auth type (jwt|api-key|oauth2|none)")
	cmd.Flags().BoolVar(&inputValidation, "input-validation", false, "whether input validation is implemented")
	cmd.Flags().BoolVar(&rateLimited, "rate-limited", false, "whether rate limiting is enabled")
	cmd.Flags().BoolVar(&encryptionAtRest, "encryption-at-rest", false, "whether data is encrypted at rest")
	cmd.Flags().BoolVar(&encryptionInTransit, "encryption-in-transit", false, "whether data is encrypted in transit")
	cmd.Flags().BoolVar(&auditLogged, "audit-logged", false, "whether actions are audit logged")
	cmd.Flags().StringVar(&threatModelStatus, "threat-model-status", "", "threat model status (not-required|pending|completed)")
	cmd.Flags().StringVar(&pentestStatus, "pentest-status", "", "pentest status (not-required|pending|passed|failed)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newSecurityPostureCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "posture <product-id>",
		Short: "Get an aggregated security overview for an entire product",
		Long: "Get an aggregated security overview for an entire product. Includes average " +
			"score, classification distribution, score ranges and a feature list with " +
			"individual scores. Ideal for security audits and product reviews.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+args[0]+"/security", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newSecurityScoreCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "score <feature-id>",
		Short:        "Get the calculated security score and release-gate state of a feature",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0]+"/security/score", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newSecurityAddTestCmd(f *Factory) *cobra.Command {
	var testType, filePath, lastRunStatus, owaspID string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "add-test <feature-id>",
		Short: "Link a security test to a feature",
		Long: "Link a security test to a feature. Security tests are specific checks like " +
			"auth-bypass, injection or XSS tests. Improves the feature's security score.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if testType == "" || filePath == "" {
				return fmt.Errorf("--type and --file are required")
			}
			payload := map[string]string{
				"securityTestType": testType,
				"filePath":         filePath,
			}
			if cmd.Flags().Changed("last-run-status") {
				payload["lastRunStatus"] = lastRunStatus
			}
			if cmd.Flags().Changed("owasp-id") {
				payload["owaspId"] = owaspID
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/security/tests", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Linked %s security test to feature %s\n", testType, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&testType, "type", "", "test type (auth-bypass|injection|access-control|rate-limit|input-validation|data-exposure|header-security|dependency-scan|csrf|xss|ssrf) (required)")
	cmd.Flags().StringVar(&filePath, "file", "", "path to the security test file (required)")
	cmd.Flags().StringVar(&lastRunStatus, "last-run-status", "", "last run status (passed|failed|skipped)")
	cmd.Flags().StringVar(&owaspID, "owasp-id", "", "OWASP Top 10 ID (e.g. A01:2021)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newSecurityReviewCmd(f *Factory) *cobra.Command {
	var reviewer, reviewStatus, reviewType, findings, conditions string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "review <feature-id>",
		Short: "Complete a security review for a feature",
		Long: "Complete a security review for a feature. When status is 'approved', the " +
			"feature is marked as security-reviewed. Required for features with " +
			"classification 'confidential' or 'restricted' before a release.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reviewer == "" || reviewStatus == "" {
				return fmt.Errorf("--reviewer and --status are required")
			}
			payload := map[string]string{
				"reviewer":     reviewer,
				"reviewStatus": reviewStatus,
				"reviewType":   reviewType,
			}
			if cmd.Flags().Changed("findings") {
				payload["findings"] = findings
			}
			if cmd.Flags().Changed("conditions") {
				payload["conditions"] = conditions
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPost, "/api/features/"+args[0]+"/security/review", body)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintf(f.Out, "Recorded %s security review for feature %s\n", reviewStatus, args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&reviewer, "reviewer", "", "reviewer identifier (required)")
	cmd.Flags().StringVar(&reviewStatus, "status", "", "review status (approved|rejected|conditional) (required)")
	cmd.Flags().StringVar(&reviewType, "type", "manual", "review type (manual|automated|external)")
	cmd.Flags().StringVar(&findings, "findings", "", "review findings")
	cmd.Flags().StringVar(&conditions, "conditions", "", "conditions for conditional approval")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newSecurityContextCmd(f *Factory, use, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:          use,
		Short:        short,
		Long:         long,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0]+"/security-context", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
}

func newSecurityAnalyzeCmd(f *Factory) *cobra.Command {
	return newSecurityContextCmd(f,
		"analyze <feature-id>",
		"Analyze the security state of a single feature in detail",
		"Analyze the security state of a single feature in detail. Returns the "+
			"security context: classification, requirements matrix, existing tests, "+
			"score and release-gate status. Use this to understand which security "+
			"requirements are met or open.",
	)
}

func newSecurityValidateCmd(f *Factory) *cobra.Command {
	return newSecurityContextCmd(f,
		"validate <feature-id>",
		"Check whether a feature meets all security requirements for a release",
		"Check if a feature meets all security requirements for a release. Returns "+
			"the security context including score, requirements and release-gate "+
			"status. Use before status transitions to 'released'.",
	)
}

func newSecuritySuggestTestsCmd(f *Factory) *cobra.Command {
	return newSecurityContextCmd(f,
		"suggest-tests <feature-id>",
		"Get security context data to help suggest appropriate security tests",
		"Get security context data for a feature to help suggest appropriate "+
			"security tests. Returns the security profile, existing tests, OWASP tags, "+
			"compliance tags and requirements.",
	)
}

func newSecurityThreatModelCmd(f *Factory) *cobra.Command {
	return newSecurityContextCmd(f,
		"threat-model <feature-id>",
		"Get security context data to help generate a STRIDE threat model",
		"Get security context data for a feature to help generate a STRIDE threat "+
			"model. Returns the feature security profile, existing threat model, "+
			"product type and security requirements.",
	)
}
