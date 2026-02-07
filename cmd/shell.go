package cmd

import (
	"github.com/spf13/cobra"
)

var shellCmd = &cobra.Command{
	Use:   "shell [path]",
	Short: "Open a zsh shell in the sandbox",
	Long:  `Open an interactive zsh shell in the sandbox. Starts the sandbox if not running.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		return runShell(resolvePath(wsPath))
	},
}

func runShell(wsPath string) error {
	name, err := ensureRunning(wsPath)
	if err != nil {
		return err
	}
	return dockerExec(name, "zsh")
}

func init() {
	rootCmd.AddCommand(shellCmd)
}
