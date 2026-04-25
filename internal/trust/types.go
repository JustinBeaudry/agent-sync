// Package trust implements the two-tier trust system described in
// docs/spec/trust-store-v1.md.
//
// It owns:
//
//   - The per-user append-only history at $XDG_DATA_HOME/aienvs/trust.jsonl.
//   - The per-user pending queue at $XDG_STATE_HOME/aienvs/pending.jsonl.
//   - The pure decision engine that maps (url, resolvedSHA, manifestTrustedSHA,
//     state, flags) to a trust Decision.
//   - The minimal interactive prompts used by the trust CLI when running
//     under a TTY.
//
// It does not own:
//
//   - The committed project pin (`trusted_sha:` in .aienv.yaml). That field
//     lives on the manifest schema; this package reads it as an input.
//   - The sync pipeline. Sync calls Decide() and acts on the returned
//     Decision; it does not import prompt.go.
package trust

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Exit codes surfaced by the trust CLI. The generic codes 1 and 2 are
// reserved for the CLI layer and never produced by this package.
const (
	ExitRevokedTrustAnchor    = 3
	ExitTrustDecisionRequired = 4
	ExitFirstUseDenied        = 5
)

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrRevokedTrustAnchor is returned when a previously revoked URL
	// reappears. Never accompanied by a prompt, even on a TTY.
	ErrRevokedTrustAnchor = errors.New("trust: revoked trust anchor reappeared")

	// ErrTrustDecisionRequired is returned when a non-interactive context
	// needs a decision it cannot make: a first-use URL with no
	// --accept-new-source, or a trusted_sha mismatch.
	ErrTrustDecisionRequired = errors.New("trust: decision required in non-interactive context")

	// ErrFirstUseDenied is returned after an interactive first-use prompt
	// was declined.
	ErrFirstUseDenied = errors.New("trust: first-use prompt declined")
)

// Kind enumerates the outcomes of the decision engine.
type Kind int

const (
	// KindProceed: no user action needed, sync continues silently.
	KindProceed Kind = iota

	// KindProceedWithReminder: known URL whose resolved SHA differs from the
	// last trusted SHA. Sync continues using the existing trusted_sha; the
	// CLI emits a one-line stderr reminder and appends to pending.jsonl.
	KindProceedWithReminder

	// KindPromptFirstURL: interactive context, URL not in user history and
	// no committed trusted_sha. Caller runs the first-URL prompt.
	KindPromptFirstURL

	// KindPromptNewSHA: interactive context, URL in user history but SHA
	// changed. Returned by trust add / promote; NEVER returned during sync
	// (sync takes KindProceedWithReminder instead — decision #9).
	KindPromptNewSHA

	// KindProceedAutoPromote: known URL with a new SHA and AllowNewSHAsOn
	// in effect (optionally within cooldown). Caller writes the promote
	// record (AppendTrustLog) and proceeds.
	KindProceedAutoPromote

	// KindRevokedBlock: URL has an active revoke. Return
	// ErrRevokedTrustAnchor.
	KindRevokedBlock

	// KindDecisionRequired: non-interactive context cannot decide; return
	// ErrTrustDecisionRequired with a Remediation message.
	KindDecisionRequired
)

// String implements fmt.Stringer for log output and test diagnostics.
func (k Kind) String() string {
	switch k {
	case KindProceed:
		return "proceed"
	case KindProceedWithReminder:
		return "proceed-with-reminder"
	case KindPromptFirstURL:
		return "prompt-first-url"
	case KindPromptNewSHA:
		return "prompt-new-sha"
	case KindProceedAutoPromote:
		return "proceed-auto-promote"
	case KindRevokedBlock:
		return "revoked-block"
	case KindDecisionRequired:
		return "decision-required"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// Decision is the structured result of the decision engine. Callers read
// Kind first and branch accordingly. Reminder and AppendPending are set only
// when Kind == KindProceedWithReminder. Remediation is set only when
// Kind == KindDecisionRequired.
type Decision struct {
	Kind Kind

	// URL is the canonical URL the decision concerns.
	URL string

	// ResolvedSHA is the SHA the caller observed and asked the engine
	// about.
	ResolvedSHA string

	// TrustedSHA is the SHA currently treated as trusted: the committed
	// trusted_sha if present, else the latest SHA in user history, else "".
	TrustedSHA string

	// Reminder is the one-line stderr message the CLI should emit when
	// Kind == KindProceedWithReminder.
	Reminder string

	// AppendPending carries the pending entry to append when
	// Kind == KindProceedWithReminder. Zero value otherwise.
	AppendPending PendingEntry

	// AppendTrustLog carries the trust-log entry the caller should append
	// when Kind == KindProceedAutoPromote. Zero value otherwise.
	AppendTrustLog LogEntry

	// Remediation is the actionable hint shown to non-interactive callers
	// when Kind == KindDecisionRequired.
	Remediation string

	// AuditEcho, when non-empty, is the one-line stderr announcement the
	// CLI must print before proceeding — used when --accept-new-source has
	// been honored (decision #10: disable the prompt, not the
	// announcement).
	AuditEcho string
}

// Op is the set of operations recorded in trust.jsonl.
type Op string

const (
	OpTrust           Op = "trust"
	OpPromote         Op = "promote"
	OpRevoke          Op = "revoke"
	OpAllowNewSHAsOn  Op = "allow-new-shas-on"
	OpAllowNewSHAsOff Op = "allow-new-shas-off"
)

// Source records the origin of a trust op. Used for audit only.
type Source string

const (
	SourceCLI    Source = "cli"
	SourceWizard Source = "wizard"
	SourceCI     Source = "ci"
)

// LogEntry is one record in trust.jsonl. See docs/spec/trust-store-v1.md.
type LogEntry struct {
	TS       time.Time `json:"-"`
	TSRaw    string    `json:"ts"`
	Op       Op        `json:"op"`
	URL      string    `json:"url"`
	SHA      string    `json:"sha"`
	PrevSHA  string    `json:"prev_sha"`
	Source   Source    `json:"source"`
	Actor    string    `json:"actor"`
	Hostname string    `json:"hostname"`

	// AllowNewSHAsCooldownSeconds encodes the optional cooldown attached to
	// an allow-new-shas-on record. Zero means indefinite (or not applicable
	// to this op).
	AllowNewSHAsCooldownSeconds int64 `json:"allow_new_shas_cooldown_seconds,omitempty"`
}

// PendingEntry is one record in pending.jsonl.
type PendingEntry struct {
	TS     time.Time `json:"-"`
	TSRaw  string    `json:"ts"`
	URL    string    `json:"url"`
	NewSHA string    `json:"new_sha"`
	OldSHA string    `json:"old_sha"`
}

// IsZero reports whether the entry has any meaningful content.
func (e PendingEntry) IsZero() bool {
	return e.URL == "" && e.NewSHA == "" && e.OldSHA == "" && e.TSRaw == ""
}

// State is the fold-over-log output for one URL.
type State struct {
	CurrentSHA                string
	LastOp                    Op
	LastOpTS                  time.Time
	Revoked                   bool
	AllowNewSHAsOn            bool
	AllowNewSHAsCooldownUntil time.Time
}

// reSHA40 matches a lowercase hex commit SHA.
var reSHA40 = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

// IsSHA40 reports whether s is a well-formed 40-lowercase-hex SHA.
func IsSHA40(s string) bool {
	return reSHA40.MatchString(s)
}

// ValidateOp reports nil when op is one of the documented vocabulary.
func ValidateOp(op Op) error {
	switch op {
	case OpTrust, OpPromote, OpRevoke, OpAllowNewSHAsOn, OpAllowNewSHAsOff:
		return nil
	default:
		return fmt.Errorf("trust: unrecognized op %q", string(op))
	}
}

// ValidateSource reports nil when s is one of the documented sources.
func ValidateSource(s Source) error {
	switch s {
	case SourceCLI, SourceWizard, SourceCI:
		return nil
	default:
		return fmt.Errorf("trust: unrecognized source %q", string(s))
	}
}

// ValidateEntry performs structural validation on a LogEntry. It does not
// verify URL canonical form (that's a warning path, not an error, per the
// spec).
func ValidateEntry(e LogEntry) error {
	if err := ValidateOp(e.Op); err != nil {
		return err
	}
	if err := ValidateSource(e.Source); err != nil {
		return err
	}
	if e.URL == "" {
		return errors.New("trust: url is required")
	}
	if e.TSRaw == "" {
		return errors.New("trust: ts is required")
	}
	switch e.Op {
	case OpTrust, OpPromote:
		if !IsSHA40(e.SHA) {
			return fmt.Errorf("trust: op %q requires a 40-hex sha, got %q", e.Op, e.SHA)
		}
	case OpRevoke, OpAllowNewSHAsOn, OpAllowNewSHAsOff:
		if e.SHA != "" {
			return fmt.Errorf("trust: op %q requires an empty sha, got %q", e.Op, e.SHA)
		}
	}
	if e.PrevSHA != "" && !IsSHA40(e.PrevSHA) {
		return fmt.Errorf("trust: prev_sha must be empty or 40-hex, got %q", e.PrevSHA)
	}
	return nil
}
