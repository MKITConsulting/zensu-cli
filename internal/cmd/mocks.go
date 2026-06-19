package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type featureMock struct {
	ID            string  `json:"id"`
	FeatureID     string  `json:"feature_id"`
	MockType      string  `json:"mock_type"`
	Title         *string `json:"title"`
	FileName      string  `json:"file_name"`
	MimeType      string  `json:"mime_type"`
	FileSizeBytes int32   `json:"file_size_bytes"`
}

func NewMocksCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mocks",
		Aliases: []string{"mock"},
		Short:   "Inspect a feature's design mocks",
		Long: "Inspect a feature's design mocks (HTML markup or image previews). Use 'list' to enumerate a " +
			"feature's mocks and 'get' to pull a single mock's metadata or raw content. Pulling an HTML mock's " +
			"raw markup is the way to load a feature's mock into your working context.",
	}
	cmd.AddCommand(
		newMocksListCmd(f),
		newMocksGetCmd(f),
	)
	return cmd
}

func newMocksListCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list <feature-id>",
		Short:        "List a feature's design mocks",
		Long:         "List a feature's design mocks. Returns each mock's type (image|html), title, file name and id.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+args[0]+"/mocks?limit=100", nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []featureMock `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "TYPE\tTITLE\tFILE_NAME\tID")
			for _, m := range env.Data {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.MockType, derefMockStr(m.Title), m.FileName, m.ID)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newMocksGetCmd(f *Factory) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "get <feature-id> <mock-id>",
		Short: "Get a mock's metadata or raw content",
		Long: "Get a single mock. By default prints the mock's metadata (type, title, file name, content type, " +
			"size). With --raw, prints the mock's raw content to stdout instead — for HTML mocks this is the " +
			"assembled markup, which lets you load the mock into your working context.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			featureID, mockID := args[0], args[1]
			if raw {
				body, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+featureID+"/mocks/"+mockID+"/raw", nil)
				if err != nil {
					return err
				}
				_, err = f.Out.Write(body)
				return err
			}

			listRaw, err := f.request(cmd.Context(), http.MethodGet, "/api/features/"+featureID+"/mocks?limit=100", nil)
			if err != nil {
				return err
			}
			var env struct {
				Data []featureMock `json:"data"`
			}
			if err := json.Unmarshal(listRaw, &env); err != nil {
				return err
			}
			for _, m := range env.Data {
				if m.ID != mockID {
					continue
				}
				fmt.Fprintf(f.Out, "ID:           %s\n", m.ID)
				fmt.Fprintf(f.Out, "Type:         %s\n", m.MockType)
				fmt.Fprintf(f.Out, "Title:        %s\n", derefMockStr(m.Title))
				fmt.Fprintf(f.Out, "File name:    %s\n", m.FileName)
				fmt.Fprintf(f.Out, "Content type: %s\n", m.MimeType)
				fmt.Fprintf(f.Out, "Size:         %d bytes\n", m.FileSizeBytes)
				return nil
			}
			return fmt.Errorf("mock %s not found for feature %s", mockID, featureID)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print the mock's raw content to stdout instead of metadata")
	return cmd
}

func derefMockStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
