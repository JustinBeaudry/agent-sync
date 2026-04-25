// Package cli holds cobra command factories for the aienvs CLI. Unit 6
// introduces the first subcommand tree (`aienvs trust`); Unit 16 will wire
// the full root command + fang styling.
package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"time"

	"github.com/adrg/xdg"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/cache"
	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/trust"
)

// yamlUnmarshal is a thin alias so we can keep the import list tidy and
// still swap out the unmarshaller from a single place if it changes.
var yamlUnmarshal = yaml.Unmarshal

// TrustDeps is the injectable dependency bundle for the trust command
// tree. Production code calls NewTrustCommand with a zero Deps; tests fill
// in the hooks they need to observe.
type TrustDeps struct {
	// Store and Pending are the persistence layers. When nil, command
	// factories resolve them from xdg.DataHome / xdg.StateHome on first use.
	Store   *trust.Store
	Pending *trust.PendingStore

	// ManifestPath overrides `.aienv.yaml` discovery. When empty the
	// command falls back to `$PWD/.aienv.yaml`.
	ManifestPath string

	// Prompter is used for interactive subcommands (add, revoke, reset).
	// When nil the factory constructs one from In/Out.
	Prompter *trust.Prompter

	// In, Out, Err override os.Stdin, os.Stdout, os.Stderr for tests.
	In  io.Reader
	Out io.Writer
	Err io.Writer

	// Now returns the reference time. Nil means time.Now.
	Now func() time.Time

	// Actor and Hostname are recorded on every LogEntry append.
	Actor    string
	Hostname string
}

// NewTrustCommand returns the `aienvs trust` cobra subtree. The caller
// (Unit 16) adds it to the root command.
func NewTrustCommand(deps TrustDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Manage the two-tier trust store",
		Long: "Manage the committed project pin (trusted_sha in .aienv.yaml) " +
			"and the per-user trust history (trust.jsonl).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(
		newTrustStatusCmd(deps),
		newTrustPendingCmd(deps),
		newTrustDiffCmd(deps),
		newTrustPromoteCmd(deps),
		newTrustPinCmd(deps),
		newTrustVerifyCmd(deps),
		newTrustResetCmd(deps),
		newTrustAddCmd(deps),
		newTrustRevokeCmd(deps),
		newTrustAllowNewSHAsCmd(deps),
		newTrustCompactCmd(deps),
	)
	return cmd
}

// --- resolvers ---

func resolveTrustStore(deps TrustDeps) (*trust.Store, error) {
	if deps.Store != nil {
		return deps.Store, nil
	}
	p, err := xdg.DataFile(filepath.Join("aienvs", "trust.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("cli: resolve trust store path: %w", err)
	}
	return trust.NewStore(p), nil
}

func resolvePendingStore(deps TrustDeps) (*trust.PendingStore, error) {
	if deps.Pending != nil {
		return deps.Pending, nil
	}
	p, err := xdg.StateFile(filepath.Join("aienvs", "pending.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("cli: resolve pending store path: %w", err)
	}
	return trust.NewPendingStore(p), nil
}

func resolveManifestPath(deps TrustDeps) string {
	if deps.ManifestPath != "" {
		return deps.ManifestPath
	}
	wd, err := os.Getwd()
	if err != nil {
		return ".aienv.yaml"
	}
	return filepath.Join(wd, ".aienv.yaml")
}

func resolvePrompter(deps TrustDeps) *trust.Prompter {
	if deps.Prompter != nil {
		return deps.Prompter
	}
	in := deps.In
	if in == nil {
		in = os.Stdin
	}
	errw := deps.Err
	if errw == nil {
		errw = os.Stderr
	}
	p := trust.NewPrompter(in, errw)
	if os.Getenv("NO_COLOR") != "" {
		p = p.WithNoColor()
	}
	return p
}

func nowFunc(deps TrustDeps) func() time.Time {
	if deps.Now != nil {
		return deps.Now
	}
	return time.Now
}

func actor(deps TrustDeps) string {
	if deps.Actor != "" {
		return deps.Actor
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

func hostname(deps TrustDeps) string {
	if deps.Hostname != "" {
		return deps.Hostname
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

func outWriter(deps TrustDeps, cmd *cobra.Command) io.Writer {
	if deps.Out != nil {
		return deps.Out
	}
	return cmd.OutOrStdout()
}

func errWriter(deps TrustDeps, cmd *cobra.Command) io.Writer {
	if deps.Err != nil {
		return deps.Err
	}
	return cmd.ErrOrStderr()
}

// --- status ---

func newTrustStatusCmd(deps TrustDeps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show per-URL trust state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			m, err := s.Fold()
			if err != nil {
				return err
			}
			out := outWriter(deps, cmd)
			return printStatus(out, m, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	return cmd
}

type statusRow struct {
	URL        string    `json:"url"`
	CurrentSHA string    `json:"current_sha"`
	LastOp     trust.Op  `json:"last_op"`
	LastOpTS   time.Time `json:"last_op_ts"`
	Revoked    bool      `json:"revoked"`
}

func printStatus(out io.Writer, m map[string]trust.State, asJSON bool) error {
	rows := make([]statusRow, 0, len(m))
	for url, st := range m {
		rows = append(rows, statusRow{
			URL:        url,
			CurrentSHA: st.CurrentSHA,
			LastOp:     st.LastOp,
			LastOpTS:   st.LastOpTS,
			Revoked:    st.Revoked,
		})
	}
	slices.SortFunc(rows, func(a, b statusRow) int { return cmp.Compare(a.URL, b.URL) })

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", r.URL, shortSHAOrDash(r.CurrentSHA), r.LastOp, r.LastOpTS.Format(time.RFC3339))
	}
	return nil
}

// --- pending ---

func newTrustPendingCmd(deps TrustDeps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List SHAs observed during sync that need review",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePendingStore(deps)
			if err != nil {
				return err
			}
			latest, err := p.Latest()
			if err != nil {
				return err
			}
			out := outWriter(deps, cmd)
			return printPending(out, latest, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	return cmd
}

func printPending(out io.Writer, latest map[string]trust.PendingEntry, asJSON bool) error {
	rows := make([]trust.PendingEntry, 0, len(latest))
	for _, e := range latest {
		rows = append(rows, e)
	}
	slices.SortFunc(rows, func(a, b trust.PendingEntry) int { return cmp.Compare(a.URL, b.URL) })
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%s\t%s -> %s\t%s\n", r.URL, shortSHAOrDash(r.OldSHA), shortSHAOrDash(r.NewSHA), r.TSRaw)
	}
	return nil
}

// --- diff ---

func newTrustDiffCmd(deps TrustDeps) *cobra.Command {
	var cacheDir string
	cmd := &cobra.Command{
		Use:   "diff <url>",
		Short: "Show git log between trusted SHA and latest observed SHA",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			if cacheDir == "" {
				return errors.New("trust diff: --cache-dir required in v1 (sync wiring lands later)")
			}
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			p, err := resolvePendingStore(deps)
			if err != nil {
				return err
			}
			state, err := s.Fold()
			if err != nil {
				return err
			}
			pend, err := p.Latest()
			if err != nil {
				return err
			}
			current := state[url].CurrentSHA
			pendingEntry, hasPending := pend[url]
			if current == "" || !hasPending {
				_, _ = fmt.Fprintf(errWriter(deps, cmd), "trust diff: no trusted/pending pair for %s\n", url)
				return nil
			}
			if !trust.IsSHA40(current) || !trust.IsSHA40(pendingEntry.NewSHA) {
				return fmt.Errorf("trust diff: invalid SHA: trusted=%q pending=%q", current, pendingEntry.NewSHA)
			}
			return runGitLogRange(cmd.Context(), cacheDir, current, pendingEntry.NewSHA, outWriter(deps, cmd))
		},
	}
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "path to the cached clone for this URL")
	return cmd
}

// runGitLogRange shells out to `git log <a>..<b>` in dir. Used by
// `trust diff`; extracted so tests can substitute a fake Cmd.
//
// Both a and b MUST be validated 40-hex SHAs before this runs — the caller
// enforces that with trust.IsSHA40 so no unsanitized argument reaches
// exec.Command. That property is what keeps gosec G204 from being a real
// hazard here.
var runGitLogRange = func(ctx context.Context, dir, a, b string, out io.Writer) error {
	// #nosec G204 — a and b are constrained to 40-hex by the caller.
	c := exec.CommandContext(ctx, "git", "-C", dir, "log", "--oneline", a+".."+b)
	c.Stdout = out
	c.Stderr = out
	return c.Run()
}

// --- promote ---

func newTrustPromoteCmd(deps TrustDeps) *cobra.Command {
	var (
		all         bool
		pinManifest bool
	)
	cmd := &cobra.Command{
		Use:   "promote [url]",
		Short: "Promote pending SHA(s) into the trust log",
		RunE: func(cmd *cobra.Command, args []string) error {
			// `--all` + `--pin-manifest` is a footgun: pending.jsonl is a
			// per-user store across many workspaces, but .aienv.yaml is a
			// per-workspace pin to a single canonical source. Looping the
			// pin-write across every pending URL would non-deterministically
			// overwrite the workspace manifest with whichever URL ran last.
			if all && pinManifest {
				return errors.New("trust promote: --all is incompatible with --pin-manifest; pin one URL at a time")
			}

			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			p, err := resolvePendingStore(deps)
			if err != nil {
				return err
			}
			latest, err := p.Latest()
			if err != nil {
				return err
			}

			var targets []string
			switch {
			case all:
				for url := range latest {
					targets = append(targets, url)
				}
			case len(args) == 1:
				targets = []string{args[0]}
			default:
				return errors.New("trust promote: pass a URL or --all")
			}

			state, err := s.Fold()
			if err != nil {
				return err
			}

			now := nowFunc(deps)()
			nowStr := now.UTC().Format(time.RFC3339)
			for _, url := range targets {
				pending, ok := latest[url]
				if !ok {
					_, _ = fmt.Fprintf(errWriter(deps, cmd), "trust promote: no pending entry for %s, skipping\n", url)
					continue
				}
				// Defense against stale pending entries: if our folded view
				// of the trust log already has a CurrentSHA for this URL, it
				// MUST match the pending entry's OldSHA, otherwise the
				// pending entry was generated against a previous trust state
				// that has since changed (someone else ran `trust add` /
				// `promote` / `revoke` in between). Recording PrevSHA from
				// the current fold would silently violate the trust.jsonl
				// chain contract.
				prevSHA := state[url].CurrentSHA
				if prevSHA != "" && pending.OldSHA != "" && prevSHA != pending.OldSHA {
					return fmt.Errorf(
						"trust promote: pending entry for %s is stale "+
							"(pending.old_sha=%s, current trusted=%s); "+
							"re-run sync to refresh the pending queue, then `aienvs trust pending`",
						url, shortSHAOrDash(pending.OldSHA), shortSHAOrDash(prevSHA),
					)
				}
				// When the local trust log has no record yet (first promote
				// for this URL on this machine), fall back to the OldSHA
				// recorded by sync — that's the chain link we want to keep.
				if prevSHA == "" {
					prevSHA = pending.OldSHA
				}
				e := trust.LogEntry{
					TS:       now,
					TSRaw:    nowStr,
					Op:       trust.OpPromote,
					URL:      url,
					SHA:      pending.NewSHA,
					PrevSHA:  prevSHA,
					Source:   trust.SourceCLI,
					Actor:    actor(deps),
					Hostname: hostname(deps),
				}
				if err := s.Append(e); err != nil {
					return err
				}
				if err := p.Clear(url); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(outWriter(deps, cmd), "promoted %s to %s\n", url, shortSHAOrDash(pending.NewSHA))

				if pinManifest {
					// Workspace safety: only pin if the URL we're promoting
					// matches the manifest's canonical URL (after
					// canonicalization on both sides). Otherwise the user is
					// promoting a pending entry from an unrelated workspace
					// and `--pin-manifest` would write an unrelated SHA into
					// the current project's .aienv.yaml.
					manifestPath := resolveManifestPath(deps)
					if err := assertPromotedURLMatchesManifest(manifestPath, url); err != nil {
						return err
					}
					if err := writePinToManifest(manifestPath, pending.NewSHA); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "promote every pending URL")
	cmd.Flags().BoolVar(&pinManifest, "pin-manifest", false, "also write trusted_sha to .aienv.yaml")
	return cmd
}

// assertPromotedURLMatchesManifest returns nil when the manifest's
// canonical.url matches promotedURL after canonicalization. It is the
// gate that keeps `trust promote --pin-manifest` from writing an
// unrelated workspace's SHA into .aienv.yaml.
//
// Local-canonical manifests (canonical.local_path) cannot be safely
// pinned via promote-from-pending; we reject in that case too.
func assertPromotedURLMatchesManifest(manifestPath, promotedURL string) error {
	m, err := manifest.LoadFile(manifestPath, manifest.LoadOptions{NonInteractive: false})
	if err != nil {
		return fmt.Errorf("trust promote: read manifest %q: %w", manifestPath, err)
	}
	if m.Canonical.URL == "" {
		return fmt.Errorf(
			"trust promote: --pin-manifest requires manifest canonical.url; "+
				"%q has no canonical url to match against", manifestPath,
		)
	}
	manifestURL, err := cache.Canonicalize(m.Canonical.URL)
	if err != nil {
		return fmt.Errorf("trust promote: canonicalize manifest url: %w", err)
	}
	promotedCanon, err := cache.Canonicalize(promotedURL)
	if err != nil {
		return fmt.Errorf("trust promote: canonicalize promoted url: %w", err)
	}
	if manifestURL != promotedCanon {
		return fmt.Errorf(
			"trust promote: --pin-manifest URL mismatch "+
				"(manifest canonical.url=%s, promoted=%s); refusing to overwrite",
			manifestURL, promotedCanon,
		)
	}
	return nil
}

// --- pin ---

func newTrustPinCmd(deps TrustDeps) *cobra.Command {
	var sha string
	cmd := &cobra.Command{
		Use:   "pin [url]",
		Short: "Write trusted_sha into .aienv.yaml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !trust.IsSHA40(sha) {
				return fmt.Errorf("trust pin: --sha must be 40-hex, got %q", sha)
			}
			return writePinToManifest(resolveManifestPath(deps), sha)
		},
	}
	cmd.Flags().StringVar(&sha, "sha", "", "40-hex SHA to pin")
	_ = cmd.MarkFlagRequired("sha")
	return cmd
}

// writePinToManifest updates BOTH canonical.commit and trusted_sha to sha.
// The manifest loader's invariant (Validate) requires them to match, so
// pin-operations write both in a single pass. The init wizard (unit 17)
// is responsible for emitting a manifest with both keys pre-declared so
// WriteResolvedSHA's "keys must exist" contract is satisfied.
func writePinToManifest(path, sha string) error {
	orig, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("trust pin: read manifest %q: %w", path, err)
	}
	updated, err := manifest.WriteResolvedSHA(orig, sha, sha)
	if err != nil {
		return fmt.Errorf("trust pin: patch manifest: %w", err)
	}
	if err := manifest.WriteFile(path, updated); err != nil {
		return fmt.Errorf("trust pin: write manifest: %w", err)
	}
	return nil
}

// --- verify ---

func newTrustVerifyCmd(deps TrustDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "CI gate: trusted_sha must match canonical.commit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := resolveManifestPath(deps)
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("verify: read manifest %q: %w", path, err)
			}
			var parsed struct {
				Canonical struct {
					Commit string `yaml:"commit"`
				} `yaml:"canonical"`
				TrustedSHA string `yaml:"trusted_sha"`
			}
			if err := yamlUnmarshal(raw, &parsed); err != nil {
				return fmt.Errorf("verify: parse manifest: %w", err)
			}
			if parsed.TrustedSHA == "" {
				return fmt.Errorf("%w: trusted_sha is required for verify", trust.ErrTrustDecisionRequired)
			}
			if parsed.Canonical.Commit == "" {
				return fmt.Errorf("%w: canonical.commit is required for verify", trust.ErrTrustDecisionRequired)
			}
			// Format check before equality: equality alone passes for
			// matched-but-malformed values like commit=trusted_sha=`main`,
			// which would let an unpinned manifest sneak past the CI gate.
			if !trust.IsSHA40(parsed.Canonical.Commit) {
				return fmt.Errorf("%w: canonical.commit must be 40-lowercase-hex, got %q",
					trust.ErrTrustDecisionRequired, parsed.Canonical.Commit)
			}
			if !trust.IsSHA40(parsed.TrustedSHA) {
				return fmt.Errorf("%w: trusted_sha must be 40-lowercase-hex, got %q",
					trust.ErrTrustDecisionRequired, parsed.TrustedSHA)
			}
			if parsed.TrustedSHA != parsed.Canonical.Commit {
				return fmt.Errorf("%w: trusted_sha=%s != canonical.commit=%s",
					trust.ErrTrustDecisionRequired, parsed.TrustedSHA, parsed.Canonical.Commit)
			}
			_, _ = fmt.Fprintln(outWriter(deps, cmd), "verify: ok")
			return nil
		},
	}
}

// --- reset ---

func newTrustResetCmd(deps TrustDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "reset <url>",
		Short: "Drop a RevokedTrustAnchor (typed-url confirmation required)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			p := resolvePrompter(deps)
			ok, err := p.TypedConfirmation("reset trust for", url)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("trust reset: confirmation mismatch, aborted")
			}
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			entries, err := s.ReadAll()
			if err != nil {
				return err
			}
			priorSHA := latestTrustedSHA(entries, url)
			if priorSHA == "" {
				return fmt.Errorf(
					"trust reset: no prior trust record found for %s; "+
						"use `aienvs trust add %s <sha>` to grant fresh trust",
					url, url,
				)
			}
			now := nowFunc(deps)()
			e := trust.LogEntry{
				TS:       now,
				TSRaw:    now.UTC().Format(time.RFC3339),
				Op:       trust.OpTrust,
				URL:      url,
				SHA:      priorSHA,
				PrevSHA:  "",
				Source:   trust.SourceCLI,
				Actor:    actor(deps),
				Hostname: hostname(deps),
			}
			return s.Append(e)
		},
	}
}

// latestTrustedSHA returns the SHA of the most recent trust or promote op
// for url, or "" if none exists. Used by reset to restore pre-revoke trust.
func latestTrustedSHA(entries []trust.LogEntry, url string) string {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.URL != url {
			continue
		}
		if e.Op == trust.OpTrust || e.Op == trust.OpPromote {
			return e.SHA
		}
	}
	return ""
}

// --- add ---

func newTrustAddCmd(deps TrustDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "add <url> <sha>",
		Short: "Manually add a trust record (typed-sha confirmation)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, sha := args[0], args[1]
			if !trust.IsSHA40(sha) {
				return fmt.Errorf("trust add: sha must be 40-hex, got %q", sha)
			}
			p := resolvePrompter(deps)
			ok, err := p.TypedConfirmation("add trust for "+url+" at", sha)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("trust add: confirmation mismatch, aborted")
			}
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			now := nowFunc(deps)()
			e := trust.LogEntry{
				TS:       now,
				TSRaw:    now.UTC().Format(time.RFC3339),
				Op:       trust.OpTrust,
				URL:      url,
				SHA:      sha,
				PrevSHA:  "",
				Source:   trust.SourceCLI,
				Actor:    actor(deps),
				Hostname: hostname(deps),
			}
			return s.Append(e)
		},
	}
}

// --- revoke ---

func newTrustRevokeCmd(deps TrustDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <url>",
		Short: "Revoke trust for a URL (typed-url confirmation)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			p := resolvePrompter(deps)
			ok, err := p.TypedConfirmation("revoke trust for", url)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("trust revoke: confirmation mismatch, aborted")
			}
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			state, err := s.Fold()
			if err != nil {
				return err
			}
			now := nowFunc(deps)()
			e := trust.LogEntry{
				TS:       now,
				TSRaw:    now.UTC().Format(time.RFC3339),
				Op:       trust.OpRevoke,
				URL:      url,
				SHA:      "",
				PrevSHA:  state[url].CurrentSHA,
				Source:   trust.SourceCLI,
				Actor:    actor(deps),
				Hostname: hostname(deps),
			}
			return s.Append(e)
		},
	}
}

// --- allow-new-shas ---

func newTrustAllowNewSHAsCmd(deps TrustDeps) *cobra.Command {
	var (
		cooldown time.Duration
		off      bool
	)
	cmd := &cobra.Command{
		Use:   "allow-new-shas <url>",
		Short: "Opt into auto-promotion for a URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			now := nowFunc(deps)()
			op := trust.OpAllowNewSHAsOn
			if off {
				op = trust.OpAllowNewSHAsOff
			}
			e := trust.LogEntry{
				TS:                          now,
				TSRaw:                       now.UTC().Format(time.RFC3339),
				Op:                          op,
				URL:                         url,
				SHA:                         "",
				PrevSHA:                     "",
				Source:                      trust.SourceCLI,
				Actor:                       actor(deps),
				Hostname:                    hostname(deps),
				AllowNewSHAsCooldownSeconds: int64(cooldown / time.Second),
			}
			return s.Append(e)
		},
	}
	cmd.Flags().DurationVar(&cooldown, "cooldown", 0, "cooldown window (e.g. 168h); 0 = indefinite")
	cmd.Flags().BoolVar(&off, "off", false, "disable allow-new-shas for url")
	return cmd
}

// --- compact ---

func newTrustCompactCmd(deps TrustDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Rotate an oversized trust.jsonl",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := resolveTrustStore(deps)
			if err != nil {
				return err
			}
			return s.Compact()
		},
	}
}

// --- small helpers ---

func shortSHAOrDash(s string) string {
	if s == "" {
		return "-"
	}
	if len(s) >= 12 {
		return s[:12]
	}
	return s
}
