package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start [path]",
	Short: "Start a sandbox for the workspace",
	Long:  `Start a sandboxed container for the given workspace directory. Builds the image on first run.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)
		sandboxRoot, _ := resolveWorkspace(wsPath)

		name, err := ensureRunning(sandboxRoot)
		if err != nil {
			return err
		}
		fmt.Printf("Sandbox %s running for %s\n", name, sandboxRoot)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
