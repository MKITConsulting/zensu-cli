package cmd

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

const discoveryTimeout = 30 * time.Second

const trustedAuthHostsEnv = "ZENSU_TRUSTED_AUTH_HOSTS"

func trustedAuthHosts(raw string) []string {
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func NewRootCmd() *cobra.Command {
	var apiURLFlag string
	f := &Factory{Out: os.Stdout}
	f.NewClient = func(ctx context.Context) (*client.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		apiURL := cfg.ResolveAPIURL(apiURLFlag, os.Getenv("ZENSU_API_URL"))
		trusted := trustedAuthHosts(os.Getenv(trustedAuthHostsEnv))
		eps := auth.DiscoverEndpoints(ctx, client.NewGuardedHTTPClient(discoveryTimeout), apiURL, trusted)
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
		NewProductsCmd(f),
		NewFeaturesCmd(f),
		NewSubfeaturesCmd(f),
		NewRoadmapCmd(f),
		NewTiersCmd(f),
		NewSecurityCmd(f),
		NewJourneysCmd(f),
		NewMocksCmd(f),
		NewDesignCmd(f),
		NewLinkCmd(f),
		NewGhostCmd(f),
		NewWikiCmd(f),
		NewDocCmd(f),
		NewKnowledgeCmd(f),
		NewPulseCmd(f),
		NewMetaCmd(f),
		NewOrgCmd(f),
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
