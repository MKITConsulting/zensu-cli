package cmd

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

func NewRootCmd() *cobra.Command {
	var apiURLFlag string
	f := &Factory{Out: os.Stdout}
	f.NewClient = func(ctx context.Context) (*client.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		apiURL := cfg.ResolveAPIURL(apiURLFlag, os.Getenv("ZENSU_API_URL"))
		eps := auth.DiscoverEndpoints(ctx, &http.Client{Timeout: 30 * time.Second}, apiURL)
		return client.New(cfg, apiURL, eps.Token), nil
	}

	root := &cobra.Command{
		Use:           "zensu",
		Short:         "Zensu CLI — manage features as first-class citizens from the terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&apiURLFlag, "api-url", "", "Zensu API base URL (overrides stored host and ZENSU_API_URL)")
	root.AddCommand(
		NewAuthCmd(f),
		NewAPICmd(f),
		NewProductsCmd(f),
		NewFeaturesCmd(f),
	)
	root.InitDefaultCompletionCmd()
	augmentCompletionHelp(root)
	return root
}

func augmentCompletionHelp(root *cobra.Command) {
	const zshSetup = `
macOS note: the default zsh ships with the completion system DISABLED, which is
the most common reason 'zensu <tab>' does nothing after installing the script.
Enable it once by adding this to ~/.zshrc (before installing), then restart zsh:

    autoload -Uz compinit; compinit

If completions still don't show up, the completion cache is stale — rebuild it:

    rm -f ~/.zcompdump*; exec zsh`

	for _, c := range root.Commands() {
		if c.Name() != "completion" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "zsh" {
				sub.Long = sub.Long + "\n" + zshSetup
			}
		}
		return
	}
}
