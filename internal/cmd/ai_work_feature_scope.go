package cmd

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func aiWorkStartGoal(f *Factory, cmd *cobra.Command, body map[string]any, product, cross, feature, revision, maintenance string, legacy bool) (string, error) {
	if legacy {
		if feature != "" || revision != "" || maintenance != "" {
			return product, fmt.Errorf("legacy replay cannot add a feature or maintenance reason")
		}
		return product, nil // The server allows exact historical retries only.
	}
	if revision != "" && feature == "" {
		return product, fmt.Errorf("--feature-revision requires --feature")
	}
	if cross != "" {
		if feature != "" || maintenance != "" {
			return product, fmt.Errorf("cross-product work cannot select a feature or maintenance reason")
		}
		return product, nil
	}
	if feature != "" {
		if maintenance != "" {
			return product, fmt.Errorf("choose --feature or --maintenance-reason, not both")
		}
		if err := aiWorkUUID(feature); err != nil {
			return product, err
		}
		if revision != "" {
			if err := aiWorkUUID(revision); err != nil {
				return product, err
			}
		}
		c, err := aiWorkClient(f, cmd)
		if err != nil {
			return product, err
		}
		goal, err := aiWorkServerGoalContext(cmd, c, product, feature, revision)
		if err != nil {
			return product, err
		}
		body["featureId"] = feature
		if revision != "" {
			body["featureRevisionId"] = revision
		}
		return goal.ProductID, nil
	}
	if strings.TrimSpace(maintenance) == "" || !utf8.ValidString(maintenance) || utf8.RuneCountInString(maintenance) > 500 || strings.IndexFunc(maintenance, unicode.IsControl) >= 0 {
		return product, fmt.Errorf("choose this goal's --feature or provide an explicit --maintenance-reason; capture cannot infer a feature")
	}
	body["maintenanceReason"] = maintenance
	return product, nil
}
