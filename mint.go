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

// apiClient builds a Tailscale API client from an OAuth client secret via the client-credentials
// flow. The secret never leaves the operator side. tailnet "-" means "the tailnet this OAuth client
// belongs to".
func apiClient(ctx context.Context, oauthSecret, baseURL string) (*tsclient.Client, error) {
	base := baseURL
	if base == "" {
		base = "https://api.tailscale.com"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("ephlink: parse base URL: %w", err)
	}
	creds := clientcredentials.Config{
		ClientID:     "ephlink",
		ClientSecret: oauthSecret,
		TokenURL:     base + "/api/v2/oauth/token",
	}
	return &tsclient.Client{Tailnet: "-", BaseURL: u, HTTP: creds.Client(ctx)}, nil
}

// Mint creates an ephemeral, single-use, tagged, pre-authorized auth key via the OAuth
// client-credentials flow. The OAuth secret stays on the caller (operator) side; only the
// resulting short-lived key is handed to a joining node.
func Mint(ctx context.Context, opts MintOptions) (string, error) {
	if opts.OAuthSecret == "" {
		return "", fmt.Errorf("ephlink: no OAuth client secret (scope: auth_keys)")
	}
	client, err := apiClient(ctx, opts.OAuthSecret, opts.BaseURL)
	if err != nil {
		return "", err
	}
	expiry := opts.Expiry
	if expiry == 0 {
		expiry = 30 * time.Minute
	}
	desc := opts.Description
	if desc == "" {
		desc = "ephlink ephemeral key"
	}

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

// Device is a tailnet member as seen via the API — a CDP-agnostic subset of the Tailscale device.
type Device struct {
	ID       string   // stable device ID (for Delete)
	Name     string   // full MagicDNS name (e.g. cdp-host-alice.tailnet.ts.net)
	Hostname string   // short hostname the node joined with (e.g. cdp-host-alice)
	Tags     []string // e.g. ["tag:ephlink-host"]
	Online   bool     // connected to control right now
}

// ListDevices returns tailnet devices, optionally filtered to those carrying tag. Pass tag="" for
// all. Uses the OAuth client (needs devices read scope).
func ListDevices(ctx context.Context, oauthSecret, baseURL, tag string) ([]Device, error) {
	if oauthSecret == "" {
		return nil, fmt.Errorf("ephlink: no OAuth client secret (scope: devices)")
	}
	client, err := apiClient(ctx, oauthSecret, baseURL)
	if err != nil {
		return nil, err
	}
	devs, err := client.Devices().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("ephlink: listing devices: %w", err)
	}
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		if tag != "" && !hasTag(d.Tags, tag) {
			continue
		}
		out = append(out, Device{
			ID:       d.ID,
			Name:     d.Name,
			Hostname: d.Hostname,
			Tags:     d.Tags,
			Online:   d.ConnectedToControl,
		})
	}
	return out, nil
}

// DeleteDevice removes a device from the tailnet by ID (uses the OAuth client; needs devices write).
func DeleteDevice(ctx context.Context, oauthSecret, baseURL, deviceID string) error {
	client, err := apiClient(ctx, oauthSecret, baseURL)
	if err != nil {
		return err
	}
	if err := client.Devices().Delete(ctx, deviceID); err != nil {
		return fmt.Errorf("ephlink: deleting device %s: %w", deviceID, err)
	}
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
