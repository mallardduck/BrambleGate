// Command bg-acceptance is BrambleGate's DNS Acceptance Suite: a standalone
// tool that scripts the real-hardware/real-network validation checks
// dev-docs/testing-guide.md otherwise walks through by hand, so re-running
// them after a major change is a command instead of a re-derivation.
//
// Deliberately its own module (dev-docs/repo-layout.md's "Why separate
// modules" sharing test): it never imports BrambleGate's internal packages,
// only talks to an already-running instance over the network, and can run
// from a different machine than the one hosting BrambleGate — e.g. from a
// laptop on a specific VLAN to prove that VLAN's split-horizon override.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:   "bg-acceptance",
		Short: "BrambleGate DNS Acceptance Suite",
	}
	root.PersistentFlags().StringVar(&configPath, "config", "acceptance.yaml", "path to acceptance config")

	root.AddCommand(newRunCmd(&configPath))
	root.AddCommand(newListCmd(&configPath))

	return root
}

func newRunCmd(configPath *string) *cobra.Command {
	var tier, scope string

	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Run acceptance checks against a live BrambleGate instance",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			all := Registry(cfg)
			selected := FilterTier(all, checks.Tier(tier))
			selected = FilterScope(selected, checks.Scope(scope))

			results := make([]checks.Result, 0, len(selected))
			for _, c := range selected {
				results = append(results, c.Run(context.Background(), cfg))
			}

			WriteTable(os.Stdout, results)

			summary := Summary(results)
			fmt.Fprintf(os.Stderr, "\n%d pass, %d fail, %d skip, %d not implemented\n",
				summary[checks.Pass], summary[checks.Fail], summary[checks.Skip], summary[checks.NotImplemented])
			if summary[checks.Fail] > 0 {
				return fmt.Errorf("%d check(s) failed", summary[checks.Fail])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tier, "tier", "", "only run this tier (network|local|mobile); default: all")
	cmd.Flags().StringVar(&scope, "scope", "", "only run this scope (protocol|bramblegate); default: all")
	return cmd
}

func newListCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List checks the current config would run, without running them",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			for _, c := range Registry(cfg) {
				fmt.Printf("%s\t[%s/%s]\n", c.Name(), c.Scope(), c.Tier())
			}
			return nil
		},
	}
}
