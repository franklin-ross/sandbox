package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Force-sync all files into a sandbox",
	Long:  `Push all configured files into a sandbox container, even if they haven't changed. Starts the sandbox if not running.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = cmd.ResolvePath(wsPath)
		sandboxRoot, _ := cmd.ResolveWorkspace(wsPath)

		name, err := cmd.EnsureStarted(sandboxRoot)
		if err != nil {
			return err
		}
		if err := cmd.SyncContainer(name, sandboxRoot, true); err != nil {
			return err
		}
		fmt.Println("Sync complete")
		return nil
	},
}

func init() {
	cmd.RootCmd.AddCommand(syncCmd)
}
