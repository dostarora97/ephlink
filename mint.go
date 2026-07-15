package ephlink

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"golang.org/x/oauth2/clientcredentials"
	tsclient "tailscale.com/client/tailscale/v2"
)

// MintOptions configures an ephemeral auth-key mint (operator side). CDP-agnostic.
type MintOptions struct {
	// OAuthSecret is a Tailscale OAuth client secret with the auth_keys write scope.
	OAuthSecret string
	// Tags the minted key (and thus the joining node) will carry, e.g. []string{"tag:ephlink-host"}.
	Tags []string
	// Expiry of the key; 0 = a sane short default (30m).
	Expiry time.Duration
	// BaseURL of the Tailscale API; empty = https://api.tailscale.com.
	BaseURL string
	// Description recorded on the key.
	Description string
}

// Mint creates an ephemeral, single-use, tagged, pre-authorized auth key via the OAuth
// client-credentials flow. The OAuth secret stays on the caller (operator) side; only the
// resulting short-lived key is handed to a joining node.
func Mint(ctx context.Context, opts MintOptions) (string, error) {
	if opts.OAuthSecret == "" {
		return "", fmt.Errorf("ephlink: no OAuth client secret (scope: auth_keys)")
	}
	base := opts.BaseURL
	if base == "" {
		base = "https://api.tailscale.com"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("ephlink: parse base URL: %w", err)
	}
	expiry := opts.Expiry
	if expiry == 0 {
		expiry = 30 * time.Minute
	}
	desc := opts.Description
	if desc == "" {
		desc = "ephlink ephemeral key"
	}

	creds := clientcredentials.Config{
		ClientID:     "ephlink",
		ClientSecret: opts.OAuthSecret,
		TokenURL:     base + "/api/v2/oauth/token",
	}
	client := &tsclient.Client{Tailnet: "-", BaseURL: u, HTTP: creds.Client(ctx)}

	var caps tsclient.KeyCapabilities
	caps.Devices.Create.Reusable = false
	caps.Devices.Create.Ephemeral = true
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = opts.Tags

	created, err := client.Keys().Create(ctx, tsclient.CreateKeyRequest{
		Capabilities:  caps,
		ExpirySeconds: int64(expiry.Seconds()),
		Description:   desc,
	})
	if err != nil {
		return "", fmt.Errorf("ephlink: minting key for tags %v: %w", opts.Tags, err)
	}
	return created.Key, nil
}
