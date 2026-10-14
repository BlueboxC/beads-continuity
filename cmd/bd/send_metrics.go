package main

import (
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
)

// Retain the old internal command as a no-op for queued upstream invocations.
var sendMetricsCmd = &cobra.Command{
	Use:    metrics.SendMetricsSubcommand,
	Short:  "Internal compatibility no-op; usage metrics are removed",
	Hidden: true,
	Args:   cobra.NoArgs,
	Run:    func(cmd *cobra.Command, args []string) {},
}

func init() { rootCmd.AddCommand(sendMetricsCmd) }
