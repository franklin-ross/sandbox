package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop [path]",
	Short: "Stop a running sandbox",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name := containerName(wsPath)
		if !isRunning(name) {
			fmt.Printf("No sandbox running for %s\n", wsPath)
			return nil
		}
		if err := dockerRun("stop", name); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
		fmt.Printf("Sandbox %s stopped\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
