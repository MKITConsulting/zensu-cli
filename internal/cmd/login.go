package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const loginTimeout = 3 * time.Minute

func newAuthLoginCmd(f *Factory) *cobra.Command {
	var apiURLFlag, withToken string
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Log in to a Zensu host",
		Long:         "Log in via the browser (OAuth2 + PKCE) or, with --with-token, an API key.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			apiURL := cfg.ResolveAPIURL(apiURLFlag, os.Getenv("ZENSU_API_URL"))
			httpClient := &http.Client{Timeout: 30 * time.Second}

			if cmd.Flags().Changed("with-token") {
				return f.loginWithToken(cmd.Context(), httpClient, cfg, apiURL, withToken, cmd.InOrStdin())
			}
			return f.loginWithBrowser(cmd.Context(), httpClient, cfg, apiURL)
		},
	}
	cmd.Flags().StringVar(&apiURLFlag, "api-url", "", "Zensu API base URL (defaults to stored host, ZENSU_API_URL, or "+config.DefaultAPIURL+")")
	cmd.Flags().StringVar(&withToken, "with-token", "", "log in with an API key (zsk_...) instead of the browser; use - to read from stdin")
	return cmd
}

func (f *Factory) loginWithToken(ctx context.Context, httpClient *http.Client, cfg *config.Config, apiURL, token string, stdin io.Reader) error {
	if token == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		return fmt.Errorf("--with-token requires an API key value (or - to read from stdin)")
	}
	if err := auth.ValidateAPIKey(ctx, httpClient, apiURL, token); err != nil {
		return fmt.Errorf("API key validation failed: %w", err)
	}
	cfg.APIURL = apiURL
	cfg.APIKey = token
	cfg.AccessToken, cfg.RefreshToken, cfg.ExpiresAt = "", "", time.Time{}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(f.Out, "Logged in to %s with API key.\n", apiURL)
	return nil
}

func (f *Factory) loginWithBrowser(ctx context.Context, httpClient *http.Client, cfg *config.Config, apiURL string) error {
	eps := auth.DiscoverEndpoints(ctx, httpClient, apiURL)
	pkce, err := auth.GeneratePKCE()
	if err != nil {
		return err
	}
	state, err := auth.GenerateState()
	if err != nil {
		return err
	}
	srv, err := auth.NewCallbackServer(state)
	if err != nil {
		return err
	}
	defer srv.Close()

	authURL := auth.AuthorizeURL(eps.Authorization, srv.RedirectURI(), pkce.Challenge, state, auth.DefaultScope)
	fmt.Fprintf(f.Out, "Opening your browser to log in. If it doesn't open, visit:\n\n  %s\n\n", authURL)
	_ = openBrowser(authURL)

	waitCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	code, err := srv.Wait(waitCtx)
	if err != nil {
		return fmt.Errorf("login was not completed: %w", err)
	}

	tok, err := auth.ExchangeCode(ctx, httpClient, eps.Token, srv.RedirectURI(), code, pkce.Verifier)
	if err != nil {
		return err
	}
	cfg.APIURL = apiURL
	cfg.APIKey = ""
	cfg.AccessToken = tok.AccessToken
	cfg.RefreshToken = tok.RefreshToken
	if tok.ExpiresIn > 0 {
		cfg.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	email, org := auth.IdentityFromToken(tok.AccessToken)
	cfg.User, cfg.Org = email, org
	if err := cfg.Save(); err != nil {
		return err
	}

	who := email
	if who == "" {
		who = apiURL
	}
	fmt.Fprintf(f.Out, "Logged in to %s as %s\n", apiURL, who)
	return nil
}
