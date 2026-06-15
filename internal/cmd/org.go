package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type orgMember struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func NewOrgCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Inspect the organization",
	}
	cmd.AddCommand(
		newOrgUsersCmd(f),
	)
	return cmd
}

func newOrgUsersCmd(f *Factory) *cobra.Command {
	var query string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "users",
		Short:        "Search the organization's users (members)",
		Long:         "Search the organization's users (members) by name or email — read-only. Returns each user's id, email, name, and role. Use this to resolve a person's name or email to their user id, then pass that id as --assignee to features create or features update to assign the feature to them. Omit --query to list all members.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "/api/members"
			if query != "" {
				q := url.Values{}
				q.Set("q", query)
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var members []orgMember
			if err := json.Unmarshal(raw, &members); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tEMAIL\tNAME\tROLE")
			for _, m := range members {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Email, m.Name, m.Role)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "name or email substring to filter by (case-insensitive); omit to list all members")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}
