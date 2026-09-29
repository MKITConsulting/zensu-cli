package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func NewAuthCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate zensu with a Zensu host",
	}
	cmd.AddCommand(newAuthLoginCmd(f), newAuthStatusCmd(f), newAuthTokenCmd(f), newAuthLogoutCmd(f))
	return cmd
}

func apiURLFlagValue(cmd *cobra.Command) string {
	if fl := cmd.Flags().Lookup("api-url"); fl != nil {
		return fl.Value.String()
	}
	return ""
}

func newAuthStatusCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "View authentication status",
		Long:         "View authentication status. Inside a work order session (ZENSU_SESSION_TOKEN) the command reports the session token and the host it talks to instead of the stored login, and never prints the token.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token, sessionHost, err := sessionTokenTarget(cfg, apiURLFlagValue(cmd))
			if err != nil {
				return err
			}
			if token != "" {
				_, err := fmt.Fprintf(f.Out, "A work order session token (%s) is active; requests go to %s with it instead of the stored login\n", sessionTokenEnv, sanitizeTerminal(sessionHost))
				return err
			}
			host := cfg.ResolveAPIURL("", "")
			switch {
			case cfg.APIKey != "":
				fmt.Fprintf(f.Out, "Logged in to %s via API key\n", host)
			case cfg.AccessToken != "":
				who := cfg.User
				if who == "" {
					if email, org := auth.IdentityFromToken(cfg.AccessToken); email != "" {
						cfg.SetIdentity(email, org)
						who = email
						_ = cfg.Save()
					} else {
						who = "(unknown user)"
					}
				}
				fmt.Fprintf(f.Out, "Logged in to %s as %s", host, who)
				if cfg.Org != "" {
					fmt.Fprintf(f.Out, " (%s)", cfg.Org)
				}
				fmt.Fprintln(f.Out)
				if !cfg.ExpiresAt.IsZero() {
					if time.Now().Before(cfg.ExpiresAt) {
						fmt.Fprintf(f.Out, "Token valid until %s\n", cfg.ExpiresAt.Local().Format(time.RFC1123))
					} else {
						fmt.Fprintln(f.Out, "Access token expired (will refresh on next request)")
					}
				}
			default:
				fmt.Fprintln(f.Out, "Not logged in. Run `zensu auth login`.")
			}
			return nil
		},
	}
}

func newAuthTokenCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "token",
		Short:        "Print the auth token for use in scripts",
		Long:         "Print the stored auth token for use in scripts. Inside a work order session (ZENSU_SESSION_TOKEN) the command refuses, so a session never reads a credential.",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if os.Getenv(sessionTokenEnv) != "" {
				return fmt.Errorf("zensu auth token prints no credential while %s is set; a work order session must not read a token", sessionTokenEnv)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			switch {
			case cfg.APIKey != "":
				fmt.Fprintln(f.Out, cfg.APIKey)
			case cfg.AccessToken != "":
				fmt.Fprintln(f.Out, cfg.AccessToken)
			default:
				return fmt.Errorf("not logged in — run `zensu auth login`")
			}
			return nil
		},
	}
}

func newAuthLogoutCmd(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Log out and remove stored credentials",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			host := cfg.ResolveAPIURL("", "")
			cleared := &config.Config{APIURL: cfg.APIURL}
			if err := cleared.Save(); err != nil {
				return err
			}
			fmt.Fprintf(f.Out, "Logged out of %s\n", host)
			return nil
		},
	}
}
