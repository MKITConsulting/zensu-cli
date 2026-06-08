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
	return root
}
