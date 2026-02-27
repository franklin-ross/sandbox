package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var stopName string

var stopCmd = &cobra.Command{
	Use:   "stop [path]",
	Short: "Stop a running sandbox",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if stopName != "" {
			if !cmd.IsRunning(stopName) {
				fmt.Printf("No sandbox named %s running\n", stopName)
				return nil
			}
			if err := cmd.DockerRun("stop", stopName); err != nil {
				return fmt.Errorf("stop container: %w", err)
			}
			fmt.Printf("Sandbox %s stopped\n", stopName)
			return nil
		}

		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = cmd.ResolvePath(wsPath)
		sandboxRoot, _ := cmd.ResolveWorkspace(wsPath)

		if sandboxRoot != wsPath {
			return fmt.Errorf("this directory uses a parent sandbox at %s\nRun 'sandbox stop' from %s instead", sandboxRoot, sandboxRoot)
		}

		name := cmd.ContainerName(sandboxRoot)
		if !cmd.IsRunning(name) {
			fmt.Printf("No sandbox running for %s\n", sandboxRoot)
			return nil
		}
		if err := cmd.DockerRun("stop", name); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
		fmt.Printf("Sandbox %s stopped\n", name)
		return nil
	},
}

func init() {
	stopCmd.Flags().StringVarP(&stopName, "name", "n", "", "stop sandbox by container name")
	cmd.RootCmd.AddCommand(stopCmd)
}
