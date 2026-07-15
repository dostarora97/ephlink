// Package envload handles explicit .env loading for the operator-side commands.
//
// Design note (security): we deliberately do NOT walk up parent directories looking for a .env.
// An earlier version searched cwd + 3 parents, which meant a binary run anywhere under a tree
// containing a .env would silently inherit that OAuth secret — surprising and a footgun. Loading
// is now explicit: the current directory, or a path the user names with --env-file. And we always
// log where the secret came from, so it is never silent.
package envload

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Load loads environment variables from a .env file, explicitly:
//   - if explicitPath != "", that file is loaded (error if missing);
//   - else if ./.env exists in the current directory, it is loaded;
//   - else nothing is loaded (the caller falls back to the ambient environment).
//
// It never searches parent directories. Existing env vars are not overridden. Load writes a short
// line to stderr saying which file (if any) it used, so credential provenance is always visible.
func Load(explicitPath string) error {
	if explicitPath != "" {
		if err := godotenv.Load(explicitPath); err != nil {
			return fmt.Errorf("--env-file %q: %w", explicitPath, err)
		}
		fmt.Fprintf(os.Stderr, "env: loaded %s\n", explicitPath)
		return nil
	}
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(".env"); err != nil {
			return fmt.Errorf("loading ./.env: %w", err)
		}
		fmt.Fprintln(os.Stderr, "env: loaded ./.env (current directory)")
		return nil
	}
	// No file — rely on the ambient environment (e.g. exported TS_OAUTH_CLIENT_SECRET).
	fmt.Fprintln(os.Stderr, "env: no ./.env; using ambient environment")
	return nil
}
