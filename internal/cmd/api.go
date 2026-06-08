package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func parseMethodPath(args []string) (method, path string) {
	if len(args) == 2 {
		return strings.ToUpper(args[0]), args[1]
	}
	return "", args[0]
}

func buildBody(fields []string, input string, stdin io.Reader) ([]byte, error) {
	if input != "" {
		if input == "-" {
			return io.ReadAll(stdin)
		}
		return os.ReadFile(input)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	obj := make(map[string]string, len(fields))
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid -f %q, expected key=value", f)
		}
		obj[k] = v
	}
	return json.Marshal(obj)
}

func NewAPICmd(f *Factory) *cobra.Command {
	var fields []string
	var input string
	cmd := &cobra.Command{
		Use:   "api [method] <path>",
		Short: "Make an authenticated request to any Zensu API endpoint",
		Long: "Make an authenticated request to any Zensu API endpoint.\n\n" +
			"Method defaults to GET, or POST when a body (-f/--input) is supplied.",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			method, path := parseMethodPath(args)
			body, err := buildBody(fields, input, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if method == "" {
				if body != nil {
					method = http.MethodPost
				} else {
					method = http.MethodGet
				}
			}
			raw, err := f.request(cmd.Context(), method, path, body)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "add a key=value field to the JSON request body")
	cmd.Flags().StringVar(&input, "input", "", "read the request body from a file (or - for stdin)")
	return cmd
}
