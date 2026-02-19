package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm [path]",
	Short: "Remove a sandbox container",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name := containerName(wsPath)
		if containerExists(name) {
			if isRunning(name) {
				if err := dockerRun("stop", name); err != nil {
					return fmt.Errorf("stop container: %w", err)
				}
			}
			if err := dockerRun("rm", name); err != nil {
				return fmt.Errorf("remove container: %w", err)
			}
			fmt.Printf("Sandbox %s removed\n", name)
		} else {
			fmt.Printf("No sandbox found for %s\n", wsPath)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(rmCmd)
}
