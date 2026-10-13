package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

const usageMetricsRemoved = "Anonymous usage metrics are removed in Beads Continuity. No usage data is collected or sent."

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Show the fork's removed usage-metrics policy",
	Long: `Beads Continuity does not collect or upload anonymous usage metrics.
The upstream metrics command names remain available for script compatibility.
Saved metrics settings and environment overrides cannot enable an uploader.

  bd metrics            show the policy
  bd metrics off        confirm that usage metrics are removed
  bd metrics on         report that enabling usage metrics is unsupported
  bd metrics example    report that no usage payload is produced`,
	Args: cobra.NoArgs,
	Run:  func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), usageMetricsRemoved) },
}

var metricsOnCmd = &cobra.Command{
	Use:          "on",
	Short:        "Report that usage metrics cannot be enabled in this fork",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("usage metrics cannot be enabled in Beads Continuity")
	},
}

var metricsOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Confirm that usage metrics are removed",
	Args:  cobra.NoArgs,
	Run:   func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), usageMetricsRemoved) },
}

var metricsExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Report that this fork produces no usage-metrics payload",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "No anonymous usage-metrics payload is produced by this fork.")
	},
}

func init() {
	metricsCmd.AddCommand(metricsOnCmd, metricsOffCmd, metricsExampleCmd)
	rootCmd.AddCommand(metricsCmd)
}
