// Command client is the operator-side tool for a fleet of ephlink CDP hosts.
//
// Hosts stay generic: each `host` does a RAW expose of its Chrome CDP port on its own mesh node
// (no CDP knowledge). `client` is the operator-side transformer + re-presenter:
//
//	client add <name>       mint an ephemeral key + print the `host` command to run on <name>'s box
//	client list             list ephlink hosts (from the Tailscale API, filtered by tag) + liveness
//	client remove <name>    remove a host's node from the tailnet
//	client serve-cdp <name>  join a node "cdp-host-<name>", dial the raw host over the mesh, apply the
//	                        CDP rewrite, and serve it under that tailnet name for consumers to attach
//
// The control commands (add/list/remove) are STATELESS: the tailnet is the source of truth (no
// local DB). They need a Tailscale OAuth client secret (TS_OAUTH_CLIENT_SECRET, or .env) with
// auth_keys + devices scopes. `serve` needs an ephemeral key for the presenting node.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"charm.land/fang/v2"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/dostarora97/ephlink"
	"github.com/dostarora97/ephlink/internal/cdp"
)

var version = "0.1.0-dev"

// hostTag is the tag every ephlink host node (and its minted key) carries.
const hostTag = "tag:ephlink-host"

// loadDotenv loads a .env file if present, searching cwd and up to 3 parent dirs. Existing env vars
// are not overridden.
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

func oauthSecret() (string, error) {
	s := os.Getenv("TS_OAUTH_CLIENT_SECRET")
	if s == "" {
		return "", fmt.Errorf("set TS_OAUTH_CLIENT_SECRET (Tailscale OAuth client secret; scopes: auth_keys, devices) — or put it in .env")
	}
	return s, nil
}

func main() {
	loadDotenv()
	root := &cobra.Command{
		Use:           "client",
		Short:         "Operator-side control plane for a fleet of ephlink CDP hosts.",
		Long:          "client manages ephlink hosts — mint keys, list the fleet, remove nodes. It is not in the CDP data path: each host self-serves its own tailnet name, which consumers connect to directly.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(addCmd(), listCmd(), removeCmd(), serveCDPCmd())

	if err := fang.Execute(context.Background(), root, fang.WithVersion(version), fang.WithNotifySignal(os.Interrupt)); err != nil {
		os.Exit(1)
	}
}

func addCmd() *cobra.Command {
	var (
		port   int
		expiry time.Duration
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Mint a key for a new host and print the command to run on its machine.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			secret, err := oauthSecret()
			if err != nil {
				return err
			}
			srcHost := "cdp-host-" + name + "-src" // the raw host node (bytes only)
			presented := "cdp-host-" + name        // the name the client will present under
			key, err := ephlink.Mint(cmd.Context(), ephlink.MintOptions{
				OAuthSecret: secret,
				Tags:        []string{hostTag},
				Expiry:      expiry,
				Description: "ephlink-host " + name, // association lives in the key (no local DB); ':' is rejected by the API
			})
			if err != nil {
				return err
			}
			// The minted key must reach <name>'s machine out-of-band; print the exact command.
			fmt.Printf("Host %q — run this on %s's machine (key expires in %s):\n\n", name, name, expiry)
			fmt.Printf("  host --authkey %s --hostname %s --operator ops\n\n", key, srcHost)
			fmt.Printf("Then, on this (operator) machine, re-present it with the CDP rewrite:\n\n")
			fmt.Printf("  client serve-cdp %s --peer %s:%d\n\n", name, srcHost, port)
			fmt.Printf("Consumers then attach from the tailnet to:\n")
			fmt.Printf("  http://%s.<your-tailnet>.ts.net:%d\n", presented, port)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "cdp-port", 9222, "CDP port the host will serve on (for the printed URL)")
	cmd.Flags().DurationVar(&expiry, "expiry", 30*time.Minute, "minted key expiry")
	return cmd
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List ephlink hosts on the tailnet (from the Tailscale API) with liveness.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := oauthSecret()
			if err != nil {
				return err
			}
			devs, err := ephlink.ListDevices(cmd.Context(), secret, "", hostTag)
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				fmt.Println("no ephlink hosts online (nothing tagged " + hostTag + ")")
				return nil
			}
			for _, d := range devs {
				status := "offline"
				if d.Online {
					status = "online"
				}
				fmt.Printf("%-28s  %-8s  %s\n", d.Hostname, status, d.Name)
			}
			return nil
		},
	}
}

func removeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a host's node from the tailnet.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			hostname := "cdp-host-" + name + "-src" // hosts join under the -src raw name
			secret, err := oauthSecret()
			if err != nil {
				return err
			}
			devs, err := ephlink.ListDevices(cmd.Context(), secret, "", hostTag)
			if err != nil {
				return err
			}
			var id string
			for _, d := range devs {
				if d.Hostname == hostname {
					id = d.ID
					break
				}
			}
			if id == "" {
				return fmt.Errorf("no ephlink host named %q (hostname %s) on the tailnet", name, hostname)
			}
			if err := ephlink.DeleteDevice(cmd.Context(), secret, "", id); err != nil {
				return err
			}
			fmt.Printf("removed host %q (%s)\n", name, hostname)
			return nil
		},
	}
}

func serveCDPCmd() *cobra.Command {
	var (
		peer     string
		port     int
		authKey  string
		printCmd bool
		expiry   time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve-cdp <name>",
		Short: "One command: mint + print the host command, then re-present it with the CDP rewrite.",
		Long: "serve-cdp is the one-command operator flow. By default it mints a host key and prints the " +
			"`host` command to run on <name>'s machine, then joins a node cdp-host-<name>, waits for the " +
			"raw host to come online, dials it over the mesh, applies the CDP Host/webSocketDebuggerUrl " +
			"rewrite, and serves it at http://cdp-host-<name>.<tailnet>.ts.net:<port>. The raw host stays " +
			"generic; all CDP logic is here, operator-side. Runs until Ctrl+C. Pass --no-print-host-cmd " +
			"if the host is already running (e.g. you used `client add` earlier).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			presented := "cdp-host-" + name
			srcHost := fmt.Sprintf("cdp-host-%s-src", name)
			if peer == "" {
				peer = fmt.Sprintf("%s:%d", srcHost, port)
			}

			// Mint + print the host command up front (unless told the host is already up).
			if printCmd {
				secret, err := oauthSecret()
				if err != nil {
					return err
				}
				hostKey, err := ephlink.Mint(cmd.Context(), ephlink.MintOptions{
					OAuthSecret: secret,
					Tags:        []string{hostTag},
					Expiry:      expiry,
					Description: "ephlink-host " + name,
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Run this on %s's machine (key expires in %s):\n\n", name, expiry)
				fmt.Fprintf(os.Stderr, "  host --authkey %s --hostname %s --operator ops\n\n", hostKey, srcHost)
				fmt.Fprintf(os.Stderr, "Waiting for %s to come online, then serving here…\n\n", srcHost)
			}

			// The presenting node's own key (independent of the host key).
			key := authKey
			if key == "" {
				key = os.Getenv("TS_AUTHKEY")
			}
			if key == "" {
				secret, err := oauthSecret()
				if err != nil {
					return fmt.Errorf("no --authkey/$TS_AUTHKEY and cannot mint: %w", err)
				}
				key, err = ephlink.Mint(cmd.Context(), ephlink.MintOptions{
					OAuthSecret: secret,
					Tags:        []string{hostTag},
					Expiry:      expiry,
					Description: "ephlink-client " + name,
				})
				if err != nil {
					return err
				}
			}

			node, err := ephlink.Join(cmd.Context(), ephlink.Config{Hostname: presented, AuthKey: key})
			if err != nil {
				return err
			}
			defer node.Close()
			ln, err := node.ListenOnMesh(port)
			if err != nil {
				return err
			}
			nodeName, ip4 := node.Name()
			advertise := fmt.Sprintf("%s:%d", nodeName, port)
			// Upstream is the raw host, reached over the mesh via this node's Dial (retries until online).
			dial := func(dctx context.Context) (net.Conn, error) { return node.Dial(dctx, peer) }
			go func() { _ = cdp.Serve(ln, advertise, peer, dial) }()

			fmt.Fprintf(os.Stderr, "serving %q at http://%s:%d  (raw host %s, ip %s)\n", name, nodeName, port, peer, ip4)
			fmt.Fprintf(os.Stderr, "  attach: chromium.connectOverCDP(\"http://%s:%d\")\n", nodeName, port)
			fmt.Fprintln(os.Stderr, "  press Ctrl+C to stop.")
			<-cmd.Context().Done()
			fmt.Fprintln(os.Stderr, "\nstopping…")
			return nil
		},
	}
	cmd.Flags().StringVar(&peer, "peer", "", "raw host to dial by MagicDNS name:port (default cdp-host-<name>-src:<port>)")
	cmd.Flags().IntVar(&port, "cdp-port", 9222, "CDP port to dial on the raw host and serve on")
	cmd.Flags().StringVar(&authKey, "authkey", "", "ephemeral mesh key for the presenting node (else $TS_AUTHKEY, else minted)")
	cmd.Flags().BoolVar(&printCmd, "print-host-cmd", true, "mint + print the `host` command to run on the target machine (set --print-host-cmd=false if the host is already running)")
	cmd.Flags().DurationVar(&expiry, "expiry", 30*time.Minute, "minted key expiry")
	return cmd
}
