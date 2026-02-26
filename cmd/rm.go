package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rmName string

var rmCmd = &cobra.Command{
	Use:   "rm [path]",
	Short: "Remove a sandbox container",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if rmName != "" {
			return removeSandbox(rmName)
		}

		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name := containerName(wsPath)
		if containerExists(name) {
			return removeSandbox(name)
		}

		// Path-based lookup failed. Check if the raw argument matches a
		// container name and hint the user toward --name.
		if len(args) > 0 && containerExists(args[0]) {
			fmt.Printf("No sandbox found for path %s\n", wsPath)
			fmt.Printf("Did you mean: sandbox rm --name %s\n", args[0])
			return nil
		}

		fmt.Printf("No sandbox found for %s\n", wsPath)
		return nil
	},
}

func removeSandbox(name string) error {
	if !containerExists(name) {
		fmt.Printf("No sandbox named %s found\n", name)
		return nil
	}
	if isRunning(name) {
		if err := dockerRun("stop", name); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
	}
	if err := dockerRun("rm", name); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	fmt.Printf("Sandbox %s removed\n", name)
	return nil
}

func init() {
	rmCmd.Flags().StringVarP(&rmName, "name", "n", "", "remove sandbox by container name instead of path")
	rootCmd.AddCommand(rmCmd)
}
