package cmd

import (
	"github.com/spf13/cobra"
)

// Not really necessary because we sync on start
var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Force-sync all files into a sandbox",
	Long:  `Push all configured files into a sandbox container, even if they haven't changed. Starts the sandbox if not running.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name, err := ensureStarted(wsPath)
		if err != nil {
			return err
		}
		return syncContainer(name, wsPath, true)
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
