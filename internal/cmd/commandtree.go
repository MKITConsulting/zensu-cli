package cmd

import (
	"regexp"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const PlaceholderArg = "6f1c2b7e-3d4a-4b5c-9d8e-7f6a5b4c3d2e"

var requiredFlagRe = regexp.MustCompile(`(?:\(|[;,] )required\)$`)

func IsRequiredFlag(flag *pflag.Flag) bool {
	if requiredFlagRe.MatchString(flag.Usage) {
		return true
	}
	annotation := flag.Annotations[cobra.BashCompOneRequiredFlag]
	return len(annotation) > 0 && annotation[0] == "true"
}

func VisitCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, child := range cmd.Commands() {
		VisitCommands(child, visit)
	}
}
