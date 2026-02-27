package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var startName string

var startCmd = &cobra.Command{
	Use:   "start [path]",
	Short: "Start a sandbox for the workspace",
	Long:  `Start a sandboxed container for the given workspace directory. Builds the image on first run.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if startName != "" {
			if cmd.IsRunning(startName) {
				fmt.Printf("Sandbox %s already running\n", startName)
				return nil
			}
			if !cmd.ContainerExists(startName) {
				return fmt.Errorf("no sandbox named %s found", startName)
			}
			if err := cmd.DockerRun("start", startName); err != nil {
				return fmt.Errorf("start container: %w", err)
			}
			fmt.Printf("Sandbox %s started\n", startName)
			return nil
		}

		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = cmd.ResolvePath(wsPath)
		sandboxRoot, _ := cmd.ResolveWorkspace(wsPath)

		name, err := cmd.EnsureRunning(sandboxRoot)
		if err != nil {
			return err
		}
		fmt.Printf("Sandbox %s running for %s\n", name, sandboxRoot)
		return nil
	},
}

func init() {
	startCmd.Flags().StringVarP(&startName, "name", "n", "", "start sandbox by container name (can only restart existing containers)")
	cmd.RootCmd.AddCommand(startCmd)
}
