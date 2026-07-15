// Package consent implements the runtime consent gate using charmbracelet/huh —
// a polished, accessible terminal form instead of a hand-rolled prompt.
//
// The copy is deliberately GENERIC: this is a tool that connects a remote operator to a Chrome
// on this machine over Tailscale. It makes no assumption about who the operator is or why they
// are connecting. Floor = explicit one-time gate (b): a single screen states exactly what is
// exposed, to whom (a free-text label the invoker supplies), for how long, and that the user can
// quit anytime, then a Confirm (Allow / Cancel). the copy discloses that a recording incl.
// session tokens may be stored on the operator's machine.
package consent

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// Request describes what the operator is asking to do, so the disclosure is honest + specific.
type Request struct {
	Operator    string // free-text label for who/what will connect (e.g. a name or team); optional
	TTL         string // human string, e.g. "30 minutes"
	RealProfile bool   // B-mode: copy of the user's real profile (exposes existing session)
	ActiveDrive bool   // operator may actively control the browser, not just observe
	CaptureNote bool   // a recording incl. session tokens may be stored on the operator's machine
}

// ErrDeclined is returned when the user does not grant consent.
var ErrDeclined = errors.New("consent declined by user")

var (
	bullet     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	titleStyle = lipgloss.NewStyle().Bold(true)
)

// description builds the styled disclosure body shown in the huh Note.
func description(req Request) string {
	ttl := req.TTL
	if strings.TrimSpace(ttl) == "" {
		ttl = "the session"
	}

	var b strings.Builder
	if op := strings.TrimSpace(req.Operator); op != "" {
		fmt.Fprintf(&b, "A remote operator (%s) is requesting to connect to a Chrome\n", titleStyle.Render(op))
	} else {
		b.WriteString("A remote operator is requesting to connect to a Chrome\n")
	}
	b.WriteString("browser on THIS computer over your Tailscale network.\n\n")
	b.WriteString(titleStyle.Render("What this allows:") + "\n")

	li := func(s string) { fmt.Fprintf(&b, "%s %s\n", bullet.Render("•"), s) }
	if req.RealProfile {
		li("Opens Chrome using a COPY of your existing profile — including your current")
		li("logins/cookies for the sites in it.")
	} else {
		li("Opens a NEW, empty Chrome window (temporary profile). You sign in fresh inside")
		li("it; your normal Chrome and its data are untouched.")
	}
	li("The operator can OBSERVE that window (pages, network, console).")
	if req.ActiveDrive {
		li("The operator can also CONTROL that window (click / type / navigate).")
	}
	if req.CaptureNote {
		li(warnStyle.Render("A recording — which can include session tokens — may be stored"))
		li(warnStyle.Render("on the operator's machine."))
	}
	fmt.Fprintf(&b, "\nDuration: about %s. You can STOP anytime by quitting (Ctrl+C or close\n", ttl)
	b.WriteString("this window). Quitting fully disconnects and cleans up.")
	return b.String()
}

// Prompt shows the consent screen and blocks until the user confirms or declines.
// Returns nil if consent granted; ErrDeclined (or a form error) otherwise.
func Prompt(req Request) error {
	var allow bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Remote Chrome connection").
				Description(description(req)),
			huh.NewConfirm().
				Title("Do you allow this connection?").
				Affirmative("Allow").
				Negative("Cancel").
				Value(&allow),
		),
	)
	if err := form.Run(); err != nil {
		return fmt.Errorf("consent form: %w", err)
	}
	if !allow {
		return ErrDeclined
	}
	return nil
}
