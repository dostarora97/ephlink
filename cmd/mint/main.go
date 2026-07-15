// Command mint is an operator-side tool that mints a short-lived, ephemeral, tagged Tailscale
// pre-authorized auth key via a Tailscale OAuth client — a thin CLI over ephlink.Mint.
//
// SECURITY: the OAuth client secret stays here (operator side), never on a joining
// node. The joining node receives only the resulting short-expiry ephemeral key.
// Credential loading is explicit: TS_OAUTH_CLIENT_SECRET from the environment, ./.env, or
// --env-file — never a parent-directory search.
//
// Usage:
//
//	TS_OAUTH_CLIENT_SECRET=tskey-client-xxx mint [--tag tag:ephlink-host] [--expiry 30m]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dostarora97/ephlink"
	"github.com/dostarora97/ephlink/internal/envload"
)

func main() {
	var (
		tag     string
		expiry  time.Duration
		envFile string
	)
	cmd := &cobra.Command{
		Use:           "mint",
		Short:         "Mint a short-lived ephemeral tagged auth key (operator side).",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Explicit credential loading: ./.env or --env-file only (never parent dirs); logs the source.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return envload.Load(envFile)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret := os.Getenv("TS_OAUTH_CLIENT_SECRET")
			if secret == "" {
				return fmt.Errorf("no TS_OAUTH_CLIENT_SECRET (Tailscale OAuth client secret, scope: auth_keys) — export it, put it in ./.env, or pass --env-file")
			}
			key, err := ephlink.Mint(cmd.Context(), ephlink.MintOptions{
				OAuthSecret: secret,
				Tags:        []string{tag},
				Expiry:      expiry,
				Description: "ephlink ephemeral key",
			})
			if err != nil {
				return err
			}
			fmt.Println(key)
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "tag:ephlink-host", "tag to assign to the minted key")
	cmd.Flags().DurationVar(&expiry, "expiry", 30*time.Minute, "key expiry")
	cmd.Flags().StringVar(&envFile, "env-file", "", "path to a .env with TS_OAUTH_CLIENT_SECRET (default: ./.env if present, else ambient env)")

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "mint:", err)
		os.Exit(1)
	}
}
