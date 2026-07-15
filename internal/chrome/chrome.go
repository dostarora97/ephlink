// Package chrome locates and launches a Chrome/Chromium instance with the CDP remote-debugging
// port enabled, using a throwaway profile. It is transport-agnostic: it only knows
// how to get a local Chrome speaking CDP on 127.0.0.1:<port>. Exposing that port to a client is the
// caller's job.
package chrome

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// candidatePaths returns likely Chrome/Chromium executable locations for the current OS.
// Order = preference. First existing + executable wins (explicit Chrome discovery).
func candidatePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "linux":
		return []string{
			"google-chrome-stable", "google-chrome", "chromium", "chromium-browser",
			"/usr/bin/google-chrome", "/usr/bin/chromium",
		}
	case "windows":
		pf := os.Getenv("ProgramFiles")
		pfx86 := os.Getenv("ProgramFiles(x86)")
		local := os.Getenv("LocalAppData")
		return []string{
			filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pfx86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
		}
	default:
		return nil
	}
}

// Find returns the path to a usable Chrome executable, or a clear error (fail loud).
func Find() (string, error) {
	for _, c := range candidatePaths() {
		// Bare names (linux) → resolve via PATH; absolute paths → stat.
		if !strings.ContainsAny(c, `/\`) {
			if p, err := exec.LookPath(c); err == nil {
				return p, nil
			}
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("no Chrome/Chromium/Edge found for %s; install Chrome or set --chrome-path", runtime.GOOS)
}

// Instance is a launched Chrome with a temp profile and an open CDP port.
type Instance struct {
	Cmd        *exec.Cmd
	Port       int
	ProfileDir string
	execPath   string
}

// LaunchOptions configures a Chrome launch.
type LaunchOptions struct {
	ExecPath   string // if empty, Find() is used
	Port       int    // CDP remote-debugging port (0 not allowed here; caller picks)
	Headless   bool   // smoke tests use headless; real sessions are headful
	ExtraArgs  []string
	StartupURL string
}

// preflightPort fails loud if the CDP port is already occupied, BEFORE we launch Chrome and
// spend 20s waiting for a "DevTools listening" line that would never come from our instance.
//
// The common cause is another Chrome already bound there — often the user's OWN running Chrome.
// Note: an already-running Chrome cannot be retro-enabled for remote debugging (chrome://inspect
// only forwards to targets that ALREADY expose a port; it does not turn one on for the browser
// you're viewing). This tool always launches its OWN Chrome with --remote-debugging-port instead,
// so a busy port means "something else is here," not "enable debugging in your Chrome."
func preflightPort(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// If we can bind it, it's free (immediately released for Chrome to take).
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		_ = ln.Close()
		return nil
	}
	return fmt.Errorf(
		"the CDP port %d is already in use on 127.0.0.1 — something is already listening there "+
			"(often another/your own running Chrome). This tool launches its OWN Chrome with the "+
			"debug port; it cannot attach to an already-running Chrome (chrome://inspect can't "+
			"enable a debug port on a live browser). Fix: quit whatever holds the port, or pass a "+
			"free --cdp-port",
		port,
	)
}

// Launch starts Chrome with a fresh temp profile and CDP enabled on 127.0.0.1:Port.
// The caller MUST call inst.Close() to tear down (idempotent cleanup).
func Launch(opts LaunchOptions) (*Instance, error) {
	execPath := opts.ExecPath
	if execPath == "" {
		p, err := Find()
		if err != nil {
			return nil, err
		}
		execPath = p
	}

	// Preflight: fail loud + early if the CDP port is already taken.
	if err := preflightPort(opts.Port); err != nil {
		return nil, err
	}

	profileDir, err := os.MkdirTemp("", "ephlink-host-profile-*")
	if err != nil {
		return nil, fmt.Errorf("create temp profile: %w", err)
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", opts.Port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	args = append(args, opts.ExtraArgs...)
	url := opts.StartupURL
	if url == "" {
		url = "about:blank"
	}
	args = append(args, url)

	cmd := exec.Command(execPath, args...)
	// Capture stderr so we can detect the "DevTools listening on ws://..." readiness line.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("start chrome: %w", err)
	}

	inst := &Instance{Cmd: cmd, Port: opts.Port, ProfileDir: profileDir, execPath: execPath}

	// Wait for Chrome to announce the DevTools endpoint (bounded).
	ready := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "DevTools listening on") {
				ready <- nil
				// keep draining so Chrome doesn't block on a full pipe
				for sc.Scan() {
				}
				return
			}
		}
		ready <- fmt.Errorf("chrome exited before opening DevTools port")
	}()

	select {
	case err := <-ready:
		if err != nil {
			_ = inst.Close()
			return nil, err
		}
	case <-time.After(20 * time.Second):
		_ = inst.Close()
		return nil, fmt.Errorf("timed out waiting for Chrome DevTools port")
	}
	return inst, nil
}

// Close kills Chrome and removes the temp profile. Idempotent + safe to call multiple times.
// Errors are best-effort — teardown must never itself hang the shutdown path.
func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	if i.Cmd != nil && i.Cmd.Process != nil {
		_ = i.Cmd.Process.Kill()
		_, _ = i.Cmd.Process.Wait()
	}
	if i.ProfileDir != "" {
		_ = os.RemoveAll(i.ProfileDir)
		i.ProfileDir = ""
	}
	return nil
}
