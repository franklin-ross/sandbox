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
		sandboxRoot, _ := resolveWorkspace(wsPath)

		if sandboxRoot != wsPath {
			return fmt.Errorf("this directory uses a parent sandbox at %s\nRun 'sandbox stop' from %s instead", sandboxRoot, sandboxRoot)
		}

		name := containerName(sandboxRoot)
		if !isRunning(name) {
			fmt.Printf("No sandbox running for %s\n", sandboxRoot)
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
