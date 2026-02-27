package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var flagHere bool

var RootCmd = &cobra.Command{
	Use:          "sandbox",
	Short:        "Manage sandboxed Claude Code containers",
	Long:         `Create, manage, and interact with Docker-based sandbox containers for Claude Code.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().BoolVar(&flagHere, "here", false, "use the exact path as the sandbox root (don't search parent directories)")
}
