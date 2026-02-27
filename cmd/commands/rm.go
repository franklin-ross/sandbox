package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var rmName string

var rmCmd = &cobra.Command{
	Use:   "rm [path]",
	Short: "Remove a sandbox container",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if rmName != "" {
			return removeSandbox(rmName)
		}

		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = cmd.ResolvePath(wsPath)
		sandboxRoot, _ := cmd.ResolveWorkspace(wsPath)

		if sandboxRoot != wsPath {
			return fmt.Errorf("this directory uses a parent sandbox at %s\nRun 'sandbox rm' from %s instead", sandboxRoot, sandboxRoot)
		}

		name := cmd.ContainerName(sandboxRoot)
		if cmd.ContainerExists(name) {
			return removeSandbox(name)
		}

		if len(args) > 0 && cmd.ContainerExists(args[0]) {
			fmt.Printf("No sandbox found for path %s\n", wsPath)
			fmt.Printf("Did you mean: sandbox rm --name %s\n", args[0])
			return nil
		}

		fmt.Printf("No sandbox found for %s\n", wsPath)
		return nil
	},
}

func removeSandbox(name string) error {
	if !cmd.ContainerExists(name) {
		fmt.Printf("No sandbox named %s found\n", name)
		return nil
	}
	if cmd.IsRunning(name) {
		if err := cmd.DockerRun("stop", name); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
	}
	if err := cmd.DockerRun("rm", name); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	fmt.Printf("Sandbox %s removed\n", name)
	return nil
}

func init() {
	rmCmd.Flags().StringVarP(&rmName, "name", "n", "", "remove sandbox by container name instead of path")
	cmd.RootCmd.AddCommand(rmCmd)
}
