package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/validate"
)

func newValidateCommand(deps RootDeps) *cobra.Command {
	var targetFilter []string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Dry-run the sync and report drift without writing anything",
		Long: "Compute what a sync would change without mutating the workspace. " +
			"Exit 0 when there is no drift, 1 when drift is detected, 2+ on an " +
			"operational error. Useful as a CI drift guard.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}
			now := deps.now()()

			prep, err := prepareEngine(cmd.Context(), rc, now)
			if err != nil {
				return fmt.Errorf("validate: %w", err)
			}
			defer prep.Close()

			req := prep.Request
			req.Options.TargetsFilter = targetFilter

			plan, err := engine.Plan(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("validate: %w", err)
			}

			if rc.Access.Output == OutputJSON {
				data, merr := validate.MarshalJSON(plan)
				if merr != nil {
					return fmt.Errorf("validate: marshal json: %w", merr)
				}
				if _, werr := fmt.Fprintln(cmd.OutOrStdout(), string(data)); werr != nil {
					return werr
				}
			} else if rerr := validate.RenderText(cmd.OutOrStdout(), plan); rerr != nil {
				return rerr
			}

			// A per-target operational error (no adapter, emit failure,
			// unreadable ledger) is recorded in TargetChange.Error with a nil
			// command error. That is an operational failure (exit 2), not clean
			// drift (exit 1) — surface it so `validate` never reports success or
			// mere "drift" when a target actually failed to plan.
			for _, t := range plan.Targets {
				if t.Error != "" {
					return &exitError{code: exitUsage, err: fmt.Errorf("validate: target %s failed: %s", t.Target, t.Error)}
				}
			}

			if code := validate.ExitCode(plan); code != validate.ExitNoDrift {
				return &exitError{code: code, err: errors.New("validate: drift detected")}
			}
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&targetFilter, "target", nil, "restrict validation to these target names (repeatable)")
	return cmd
}
