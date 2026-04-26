package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/aienvs/aienvs/internal/adapter/conformance"
)

const (
	conformanceVersion       = "aienvs/v1"
	defaultCLIConformanceRE  = "^(happy|spec-example)-"
	reportFormatJSON         = "json"
	reportFormatText         = "text"
	exitCodeConformanceFail  = 1
	exitCodeConformanceSpawn = 2
)

type AdapterDeps struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	Now func() time.Time
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e == nil || e.err == nil {
		return fmt.Sprintf("cli: exit %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *exitError) ExitCode() int {
	if e == nil {
		return 0
	}
	return e.code
}

type jsonReport struct {
	Cases   []conformance.CaseResult `json:"cases"`
	Summary conformance.Summary      `json:"summary"`
	Version string                   `json:"version"`
}

func NewAdapterCommand(deps AdapterDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "adapter",
		Short:         "Run adapter-focused developer workflows",
		Long:          "Run adapter conformance checks and other adapter-specific developer workflows.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newAdapterConformanceTestCmd(deps))
	return cmd
}

func newAdapterConformanceTestCmd(deps AdapterDeps) *cobra.Command {
	var (
		verbose bool
		format  string
		filter  string
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "conformance-test <binary>",
		Short: "Run the frozen adapter conformance corpus against a binary",
		Long: "Run the positive `aienvs/v1` adapter conformance fixtures against a binary. " +
			"Use --filter='.*' to include internal adversarial fixtures.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binary, err := resolveBinaryPath(args[0])
			if err != nil {
				_, _ = fmt.Fprintf(errWriterAdapter(deps, cmd), "adapter conformance spawn error: %v\n", err)
				return &exitError{code: exitCodeConformanceSpawn, err: fmt.Errorf("resolve binary %q: %w", args[0], err)}
			}

			compiled, err := regexp.Compile(filter)
			if err != nil {
				return fmt.Errorf("compile filter %q: %w", filter, err)
			}

			cases, err := filteredConformanceCases(compiled)
			if err != nil {
				return err
			}

			opts := conformance.RunOptions{
				Cases:   cases,
				Verbose: verbose,
			}
			if timeout > 0 {
				opts.Timeouts.Handshake = timeout
				opts.Timeouts.Emit = timeout
				opts.Timeouts.Shutdown = timeout
			}

			report, err := conformance.Run(cmd.Context(), binary, opts)
			if err != nil {
				_, _ = fmt.Fprintf(errWriterAdapter(deps, cmd), "adapter conformance spawn error: %v\n", err)
				return &exitError{code: exitCodeConformanceSpawn, err: fmt.Errorf("run conformance: %w", err)}
			}

			if err := renderConformanceReport(outWriterAdapter(deps, cmd), report, format, verbose); err != nil {
				return err
			}
			if report.Summary.Failed > 0 {
				return &exitError{code: exitCodeConformanceFail, err: errors.New("adapter conformance failed")}
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "include failure detail in the report")
	cmd.Flags().StringVar(&format, "format", reportFormatText, "output format: text or json")
	cmd.Flags().StringVar(&filter, "filter", defaultCLIConformanceRE, "regular expression selecting corpus cases by name")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "override handshake, emit, and shutdown timeouts for every case")
	return cmd
}

func resolveBinaryPath(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory", binary)
	}
	return path, nil
}

func filteredConformanceCases(filter *regexp.Regexp) ([]conformance.Case, error) {
	all, err := conformance.LoadCorpus()
	if err != nil {
		return nil, err
	}
	cases := make([]conformance.Case, 0, len(all))
	for _, tc := range all {
		if filter.MatchString(tc.Name) {
			cases = append(cases, tc)
		}
	}
	return cases, nil
}

func renderConformanceReport(w io.Writer, report conformance.Report, format string, verbose bool) error {
	switch format {
	case reportFormatJSON:
		payload := jsonReport{
			Cases:   report.Cases,
			Summary: report.Summary,
			Version: conformanceVersion,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	case reportFormatText:
		return renderTextConformanceReport(w, report, verbose)
	default:
		return fmt.Errorf("unsupported format %q (want json|text)", format)
	}
}

func renderTextConformanceReport(w io.Writer, report conformance.Report, verbose bool) error {
	results := slices.Clone(report.Cases)
	slices.SortFunc(results, func(a, b conformance.CaseResult) int {
		return cmp.Compare(a.Name, b.Name)
	})

	for _, result := range results {
		line := fmt.Sprintf("%s %s", result.Status, result.Name)
		if result.Reason != "" {
			line += ": " + result.Reason
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		if verbose && len(result.ActualOps) > 0 {
			data, err := json.Marshal(result.ActualOps)
			if err != nil {
				return fmt.Errorf("marshal actual ops for %q: %w", result.Name, err)
			}
			if _, err := fmt.Fprintf(w, "  actual_ops=%s\n", data); err != nil {
				return err
			}
		}
	}

	_, err := fmt.Fprintf(
		w,
		"summary total=%d passed=%d failed=%d skipped=%d version=%s\n",
		report.Summary.Total,
		report.Summary.Passed,
		report.Summary.Failed,
		report.Summary.Skipped,
		conformanceVersion,
	)
	return err
}

func outWriterAdapter(deps AdapterDeps, cmd *cobra.Command) io.Writer {
	if deps.Out != nil {
		return deps.Out
	}
	return cmd.OutOrStdout()
}

func errWriterAdapter(deps AdapterDeps, cmd *cobra.Command) io.Writer {
	if deps.Err != nil {
		return deps.Err
	}
	return cmd.ErrOrStderr()
}
