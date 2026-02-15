package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update [path]",
	Short: "Force-sync all files into a running sandbox",
	Long:  `Push the workflow binary, entrypoint, firewall script, and ZSH theme into a running sandbox container, even if they haven't changed.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name := containerName(wsPath)
		if !isRunning(name) {
			return fmt.Errorf("no sandbox running for %s", wsPath)
		}

		return syncContainer(name, true)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
