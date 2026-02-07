package cmd

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List running sandboxes",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := exec.Command("docker", "ps",
			"--filter", "label="+labelSel,
			"--format", `table {{.Names}}\t{{.Status}}\t{{.Label "`+labelWs+`"}}`)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	rootCmd.AddCommand(lsCmd)
}
