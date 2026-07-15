// Command agent is the machine-side handler (cmd/agent).
//
// It runs on the machine whose Chrome will be connected to. Generic: it makes no assumption
// about who connects or why.
//
// Flow: consent gate → launch Chrome with a fresh temp profile + CDP port →
// join the ephemeral mesh (ephlink) and expose the CDP port by MagicDNS name →
// hold the session open → idempotent teardown on quit.
//
// CLI: cobra + charmbracelet/fang (styled help/errors/version + signal handling). Transport is
// the ephlink library (embedded tsnet) — no hand-rolled networking, no system Tailscale.
package main

import (
	"context"
	"fmt"
	"os"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/dostarora97/ephlink"
	"github.com/dostarora97/ephlink/internal/chrome"
	"github.com/dostarora97/ephlink/internal/consent"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

type config struct {
	operator, ttl, chromePath          string
	authKey, hostname                  string
	port                               int
	headless, realProfile, activeDrive bool
	skipConsent, localOnly             bool
}

func newRootCmd() *cobra.Command {
	cfg := &config{}
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Expose this machine's Chrome to a remote peer over an ephemeral mesh (CDP).",
		Long: "agent launches a Chrome instance with the DevTools Protocol enabled and exposes it " +
			"on an ephemeral Tailscale mesh (via embedded tsnet — no separate Tailscale install " +
			"needed), then tears everything down when you quit. A consent gate is shown before " +
			"anything is exposed.",
		Example: "  agent --operator ops --authkey $KEY   # join the mesh + expose Chrome\n" +
			"  agent --local-only                    # loopback only (local testing)\n" +
			"  agent --yes --headless --authkey $KEY # supervised automation / smoke test",
		SilenceUsage:  true, // fang renders errors; don't dump usage on RunE error
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), cfg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.operator, "operator", "", "free-text label for who/what is connecting (shown in the consent prompt)")
	f.StringVar(&cfg.ttl, "ttl", "30 minutes", "human-readable session duration (shown in the consent prompt)")
	f.IntVar(&cfg.port, "cdp-port", 9222, "local CDP remote-debugging port for the launched Chrome")
	f.BoolVar(&cfg.headless, "headless", false, "run Chrome headless (smoke tests only; real sessions are headful)")
	f.StringVar(&cfg.chromePath, "chrome-path", "", "override the Chrome executable path (else auto-detect)")
	f.BoolVar(&cfg.realProfile, "real-profile", false, "B-mode: copy the user's real profile (exposes existing session) [not implemented]")
	f.BoolVar(&cfg.activeDrive, "active", true, "allow the operator to actively control (not just observe)")
	f.BoolVar(&cfg.skipConsent, "yes", false, "skip the interactive consent prompt (supervised automation / smoke tests)")
	f.BoolVar(&cfg.localOnly, "local-only", false, "do not expose on the tailnet (loopback CDP only; for local testing)")
	f.StringVar(&cfg.authKey, "authkey", "", "ephemeral mesh auth key (from the `mint` tool / $TS_AUTHKEY)")
	f.StringVar(&cfg.hostname, "hostname", "cdp-agent", "MagicDNS hostname for this node on the mesh")
	return cmd
}

func main() {
	// fang: styled help/errors/version + Ctrl+C handling → cancels cmd.Context().
	if err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithVersion(version),
		fang.WithNotifySignal(os.Interrupt),
	); err != nil {
		os.Exit(1)
	}
}

// run holds the orchestration; ctx is cancelled on Ctrl+C (via fang's signal handling).
func run(ctx context.Context, cfg *config) error {
	if cfg.realProfile {
		return fmt.Errorf("--real-profile (B-mode) not implemented; use the default temp profile")
	}

	// 1. Consent gate — before anything is exposed.
	if !cfg.skipConsent {
		if err := consent.Prompt(consent.Request{
			Operator:    cfg.operator,
			TTL:         cfg.ttl,
			RealProfile: cfg.realProfile,
			ActiveDrive: cfg.activeDrive,
			CaptureNote: true, // disclose that a recording incl. tokens may be stored
		}); err != nil {
			return err
		}
	}

	// 2. Launch Chrome (temp profile + CDP port).
	fmt.Fprintln(os.Stderr, "launching Chrome…")
	inst, err := chrome.Launch(chrome.LaunchOptions{
		ExecPath: cfg.chromePath,
		Port:     cfg.port,
		Headless: cfg.headless,
	})
	if err != nil {
		return err
	}
	defer inst.Close() // idempotent cleanup
	fmt.Fprintf(os.Stderr, "Chrome up: CDP on 127.0.0.1:%d (temp profile %s)\n", inst.Port, inst.ProfileDir)

	// 3. TRANSPORT: join the ephemeral mesh (ephlink) and expose the CDP port by name.
	//    --local-only skips the mesh (loopback CDP only, for local testing).
	if cfg.localOnly {
		fmt.Fprintf(os.Stderr, "local-only mode: CDP reachable at 127.0.0.1:%d (no mesh exposure)\n", cfg.port)
	} else {
		key := cfg.authKey
		if key == "" {
			key = os.Getenv("TS_AUTHKEY")
		}
		if key == "" {
			return fmt.Errorf("no auth key: pass --authkey (or $TS_AUTHKEY); mint one with the `mint` tool, or use --local-only")
		}
		node, err := ephlink.Join(ctx, ephlink.Config{Hostname: cfg.hostname, AuthKey: key})
		if err != nil {
			return err
		}
		defer node.Close() // ephemeral node auto-deregisters; closes listeners + server
		if err := node.Expose(fmt.Sprintf("127.0.0.1:%d", cfg.port), cfg.port); err != nil {
			return err
		}
		host, ip4 := node.Name()
		fmt.Fprintf(os.Stderr, "joined mesh as %q (ephemeral) — hub connects by MagicDNS name: --peer %s:%d  (ip %s)\n", host, host, cfg.port, ip4)
	}

	// 4. Hold open until the context is cancelled (Ctrl+C via fang); defers tear down.
	fmt.Fprintln(os.Stderr, "\nsession active — press Ctrl+C to stop and clean up.")
	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nstopping — tearing down…")
	return nil
}
