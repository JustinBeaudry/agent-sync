package trust

import (
	"errors"
	"fmt"
	"time"
)

// DecideFlags carries the CLI flag state that influences a trust decision.
// All fields zero-valued means "plain sync, no overrides".
type DecideFlags struct {
	// AcceptNewSource, if non-empty, is the SHA the caller promised to
	// accept via --accept-new-source=<sha>. Policy requires it to equal
	// ResolvedSHA or ErrTrustDecisionRequired is returned.
	AcceptNewSource string

	// AcceptAny reflects --accept-new-source=any. Requires AcceptAnyPeerGate
	// (from AGENT_SYNC_ALLOW_UNSAFE_ANY=1 env or --i-understand-this-is-dangerous)
	// to take effect; otherwise ErrTrustDecisionRequired is returned.
	AcceptAny         bool
	AcceptAnyPeerGate bool
}

// DecideInput is the full context the engine needs to make a pure
// decision. Every field is an input; there is no global state.
type DecideInput struct {
	// URL is the canonical URL of the source (see internal/cache.Canonicalize).
	URL string

	// ResolvedSHA is the 40-hex SHA the Git layer resolved.
	ResolvedSHA string

	// ManifestTrustedSHA is the value of `trusted_sha:` from `.agent-sync.yaml`.
	// Empty string means absent.
	ManifestTrustedSHA string

	// State is the fold-over-log entry for URL. Zero value means "no user
	// history".
	State State

	// StateLoaded asserts that State was populated from the trust store
	// (Store.Fold) rather than left zero-valued. The PostureAllowNewSHAs
	// auto path refuses to run unless this is true: a zero State has
	// Revoked==false, so honoring the auto posture without a loaded State
	// would silently skip the revoke check above. Making the posture
	// require StateLoaded keeps the revoke guarantee structural rather than
	// dependent on each caller remembering to Fold.
	StateLoaded bool

	// Posture selects the trust posture for this decision. Zero value keeps
	// today's gated behavior; PostureAllowNewSHAs enables the auto-advance
	// posture for manifest-pin drift when FastForward is also true.
	Posture Posture

	// FastForward tells the policy engine whether the caller already proved
	// the new SHA is a descendant of the current trusted pin. The auto
	// posture only activates when this is true.
	FastForward bool

	// TTY reports whether the process has an interactive stdin+stderr. When
	// false, policy never chooses a Prompt* Kind.
	TTY bool

	// Flags carries CLI flag state.
	Flags DecideFlags

	// Now is the reference time for cooldown checks. Callers inject
	// time.Now() at the edge; tests pin it.
	Now time.Time

	// Actor/Hostname/Source are carried so policy can build LogEntry /
	// PendingEntry records without the caller re-doing that plumbing.
	Actor    string
	Hostname string
	Source   Source
}

// ExitCodeFor maps a trust error to its documented CLI exit code. nil → 0.
// Unknown errors → 1 (generic failure).
func ExitCodeFor(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrRevokedTrustAnchor):
		return ExitRevokedTrustAnchor
	case errors.Is(err, ErrTrustDecisionRequired):
		return ExitTrustDecisionRequired
	case errors.Is(err, ErrFirstUseDenied):
		return ExitFirstUseDenied
	default:
		return 1
	}
}

// Decide is the pure trust-decision engine. Given a fully populated
// DecideInput, it returns a Decision plus (for block cases) a sentinel
// error the caller can `errors.Is` against.
//
// Ordering of checks matters:
//
//  1. Revoke is absolute — even a TTY never overrides it.
//  2. ManifestTrustedSHA is authoritative when present: match proceeds,
//     mismatch either fails closed (default posture) or proceeds through
//     the explicit allow-new-shas posture when the caller also proved a
//     fast-forward.
//  3. User history is consulted next. Known+same proceeds. Known+new
//     either auto-promotes (AllowNewSHAs with active cooldown) or yields
//     KindProceedWithReminder (plan decision #9: no mid-sync prompt).
//  4. Unknown URL: CI needs --accept-new-source (exact SHA or =any+peer
//     gate); TTY gets KindPromptFirstURL.
func Decide(in DecideInput) (Decision, error) {
	d := Decision{
		URL:         in.URL,
		ResolvedSHA: in.ResolvedSHA,
		TrustedSHA:  effectiveTrustedSHA(in),
	}

	// 1. Revoke beats everything.
	if in.State.Revoked {
		d.Kind = KindRevokedBlock
		return d, fmt.Errorf("%w: %s", ErrRevokedTrustAnchor, in.URL)
	}

	// 1b. The allow-new-shas auto path is fail-closed without loaded trust
	// state. A zero State has Revoked==false, so proceeding here would skip
	// the revoke check above. Refuse structurally rather than trusting the
	// caller to have Folded the store.
	if in.Posture == PostureAllowNewSHAs && !in.StateLoaded {
		d.Kind = KindDecisionRequired
		return d, fmt.Errorf("%w: allow-new-shas posture requires loaded trust state", ErrTrustDecisionRequired)
	}

	// 2. Manifest pin authoritative when set.
	if in.ManifestTrustedSHA != "" {
		if in.ManifestTrustedSHA == in.ResolvedSHA {
			d.Kind = KindProceed
			return d, nil
		}
		if in.Posture == PostureAllowNewSHAs && in.FastForward {
			d.Kind = KindProceedAutoAdvance
			d.TrustedSHA = in.ResolvedSHA
			d.AppendPending = PendingEntry{
				TS:     in.Now,
				TSRaw:  in.Now.UTC().Format(time.RFC3339),
				URL:    in.URL,
				NewSHA: in.ResolvedSHA,
				OldSHA: in.ManifestTrustedSHA,
			}
			return d, nil
		}
		// Pin mismatch: CI fails closed. For a TTY, sync still uses the pin
		// and emits a reminder so the user notices a newer SHA exists —
		// this matches plan decision #9's "sync no longer prompts mid-sync"
		// rule and reuses the pending-review flow.
		if !in.TTY {
			d.Kind = KindDecisionRequired
			d.Remediation = fmt.Sprintf(
				"trusted_sha (%s) does not match resolved SHA (%s). "+
					"Run `agent-sync trust promote %s --pin-manifest` to accept, or "+
					"update canonical.commit to match.",
				shortSHA(in.ManifestTrustedSHA), shortSHA(in.ResolvedSHA), in.URL,
			)
			return d, fmt.Errorf("%w: manifest pin %s != resolved %s",
				ErrTrustDecisionRequired, shortSHA(in.ManifestTrustedSHA), shortSHA(in.ResolvedSHA))
		}
		// TTY + pin + drift → reminder + pending, using the pin.
		d.Kind = KindProceedWithReminder
		d.Reminder = fmt.Sprintf(
			"Newer SHA available for %s: %s (currently pinned to %s). "+
				"Run `agent-sync trust pending` to review.",
			in.URL, shortSHA(in.ResolvedSHA), shortSHA(in.ManifestTrustedSHA),
		)
		d.AppendPending = PendingEntry{
			TS:     in.Now,
			TSRaw:  in.Now.UTC().Format(time.RFC3339),
			URL:    in.URL,
			NewSHA: in.ResolvedSHA,
			OldSHA: in.ManifestTrustedSHA,
		}
		return d, nil
	}

	// 3. User history.
	if in.State.LastOp != "" && in.State.CurrentSHA != "" {
		if in.State.CurrentSHA == in.ResolvedSHA {
			d.Kind = KindProceed
			return d, nil
		}
		// Known URL, new SHA.
		if in.State.AllowNewSHAsOn && cooldownActive(in) {
			d.Kind = KindProceedAutoPromote
			// Auto-promote: the caller will append the promote record and the
			// new SHA becomes the effective trust. Surface that to the caller
			// via TrustedSHA so a natural read of d.TrustedSHA matches what
			// the system now treats as trusted.
			d.TrustedSHA = in.ResolvedSHA
			d.AppendTrustLog = LogEntry{
				TS:       in.Now,
				TSRaw:    in.Now.UTC().Format(time.RFC3339),
				Op:       OpPromote,
				URL:      in.URL,
				SHA:      in.ResolvedSHA,
				PrevSHA:  in.State.CurrentSHA,
				Source:   sourceOrDefault(in.Source),
				Actor:    in.Actor,
				Hostname: in.Hostname,
			}
			return d, nil
		}
		if !in.TTY {
			// Non-interactive: honor the documented CI escape hatches before
			// failing closed. This mirrors the first-URL CI branch below so
			// `--accept-new-source=<sha>` and `--accept-new-source=any` (with
			// peer gate) work for SHA drift on previously trusted URLs — which
			// is exactly what the remediation message points users at.
			if in.Flags.AcceptNewSource != "" {
				if in.Flags.AcceptNewSource != in.ResolvedSHA {
					d.Kind = KindDecisionRequired
					d.Remediation = fmt.Sprintf(
						"--accept-new-source=%s but resolved SHA is %s. "+
							"Update the flag to match, or omit it to fail closed.",
						shortSHA(in.Flags.AcceptNewSource), shortSHA(in.ResolvedSHA),
					)
					return d, fmt.Errorf("%w: --accept-new-source mismatch", ErrTrustDecisionRequired)
				}
				d.Kind = KindProceed
				d.AuditEcho = auditLine(in.URL, in.ResolvedSHA)
				return d, nil
			}
			if in.Flags.AcceptAny {
				if !in.Flags.AcceptAnyPeerGate {
					d.Kind = KindDecisionRequired
					d.Remediation = "--accept-new-source=any requires AGENT_SYNC_ALLOW_UNSAFE_ANY=1 or --i-understand-this-is-dangerous."
					return d, fmt.Errorf("%w: accept-any peer gate missing", ErrTrustDecisionRequired)
				}
				d.Kind = KindProceed
				d.AuditEcho = auditLine(in.URL, in.ResolvedSHA)
				return d, nil
			}
			// No pin, no flag override: fail closed.
			d.Kind = KindDecisionRequired
			d.Remediation = fmt.Sprintf(
				"known URL %s changed SHA (%s -> %s). "+
					"Commit `trusted_sha: %s` to .agent-sync.yaml, "+
					"or rerun with --accept-new-source=%s.",
				in.URL, shortSHA(in.State.CurrentSHA), shortSHA(in.ResolvedSHA),
				in.ResolvedSHA, in.ResolvedSHA,
			)
			return d, fmt.Errorf("%w: known URL %s drifted sha", ErrTrustDecisionRequired, in.URL)
		}
		// Interactive + no pin + drift → reminder + pending (plan decision #9).
		d.Kind = KindProceedWithReminder
		d.Reminder = fmt.Sprintf(
			"Newer SHA available for %s: %s (previously trusted %s). "+
				"Run `agent-sync trust pending` to review.",
			in.URL, shortSHA(in.ResolvedSHA), shortSHA(in.State.CurrentSHA),
		)
		d.AppendPending = PendingEntry{
			TS:     in.Now,
			TSRaw:  in.Now.UTC().Format(time.RFC3339),
			URL:    in.URL,
			NewSHA: in.ResolvedSHA,
			OldSHA: in.State.CurrentSHA,
		}
		return d, nil
	}

	// 4. First-URL for this user.
	// CI branch: --accept-new-source=<sha> matching, or --accept-new-source=any+peer.
	if !in.TTY {
		if in.Flags.AcceptNewSource != "" {
			if in.Flags.AcceptNewSource != in.ResolvedSHA {
				d.Kind = KindDecisionRequired
				d.Remediation = fmt.Sprintf(
					"--accept-new-source=%s but resolved SHA is %s. "+
						"Update the flag to match, or omit it to fail closed.",
					shortSHA(in.Flags.AcceptNewSource), shortSHA(in.ResolvedSHA),
				)
				return d, fmt.Errorf("%w: --accept-new-source mismatch", ErrTrustDecisionRequired)
			}
			d.Kind = KindProceed
			d.AuditEcho = auditLine(in.URL, in.ResolvedSHA)
			return d, nil
		}
		if in.Flags.AcceptAny {
			if !in.Flags.AcceptAnyPeerGate {
				d.Kind = KindDecisionRequired
				d.Remediation = "--accept-new-source=any requires AGENT_SYNC_ALLOW_UNSAFE_ANY=1 or --i-understand-this-is-dangerous."
				return d, fmt.Errorf("%w: accept-any peer gate missing", ErrTrustDecisionRequired)
			}
			d.Kind = KindProceed
			d.AuditEcho = auditLine(in.URL, in.ResolvedSHA)
			return d, nil
		}
		d.Kind = KindDecisionRequired
		d.Remediation = fmt.Sprintf(
			"first use of %s (%s). Rerun interactively, or pass "+
				"--accept-new-source=%s, or commit `trusted_sha: %s` to .agent-sync.yaml.",
			in.URL, shortSHA(in.ResolvedSHA), in.ResolvedSHA, in.ResolvedSHA,
		)
		return d, fmt.Errorf("%w: first use of %s", ErrTrustDecisionRequired, in.URL)
	}

	// Interactive first URL: caller runs the prompt.
	d.Kind = KindPromptFirstURL
	return d, nil
}

// effectiveTrustedSHA returns the SHA the policy treats as "current trust".
// Manifest pin takes precedence; otherwise the user history's current SHA.
func effectiveTrustedSHA(in DecideInput) string {
	if in.ManifestTrustedSHA != "" {
		return in.ManifestTrustedSHA
	}
	return in.State.CurrentSHA
}

// cooldownActive reports whether allow-new-shas is still within its
// cooldown window (or indefinite when the zero value is used).
func cooldownActive(in DecideInput) bool {
	if in.State.AllowNewSHAsCooldownUntil.IsZero() {
		return true // indefinite
	}
	return in.Now.Before(in.State.AllowNewSHAsCooldownUntil)
}

func sourceOrDefault(s Source) Source {
	if s == "" {
		return SourceCLI
	}
	return s
}

// auditLine is the stderr announcement emitted for --accept-new-source
// usage (decision #10).
func auditLine(url, sha string) string {
	return fmt.Sprintf("Trusting new source: URL=%s SHA=%s", url, sha)
}

// shortSHA renders the first 12 chars of a SHA, or the full value if
// shorter. Used only in user-facing messages.
func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}
