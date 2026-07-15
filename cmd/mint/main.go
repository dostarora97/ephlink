// Command mint is an operator-side tool that mints a short-lived, ephemeral, tagged Tailscale
// pre-authorized auth key via a Tailscale OAuth client — a thin CLI over ephlink.Mint.
//
// SECURITY: the OAuth client secret stays here (operator side), never on a joining
// node. The joining node receives only the resulting short-expiry ephemeral key.
//
// Usage:
//
//	TS_OAUTH_CLIENT_SECRET=tskey-client-xxx mint [--tag tag:ephlink-host] [--expiry 30m]
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/dostarora97/ephlink"
)

// loadDotenv loads a .env file if present, searching cwd and up to 3 parent dirs (so it works
// whether you run from the repo root or a subdir). Existing env vars are NOT overridden.
func loadDotenv() {
	dir, _ := os.Getwd()
	for i := 0; i < 4 && dir != "" && dir != "/"; i++ {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
		dir = filepath.Dir(dir)
	}
}

func main() {
	loadDotenv()
	var (
		tag    string
		expiry time.Duration
	)
	cmd := &cobra.Command{
		Use:           "mint",
		Short:         "Mint a short-lived ephemeral tagged auth key (operator side).",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret := os.Getenv("TS_OAUTH_CLIENT_SECRET")
			if secret == "" {
				return fmt.Errorf("set TS_OAUTH_CLIENT_SECRET (Tailscale OAuth client secret, scope: auth_keys)")
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

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "mint:", err)
		os.Exit(1)
	}
}
