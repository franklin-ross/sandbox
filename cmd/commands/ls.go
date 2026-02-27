package commands

import (
	"os"
	"os/exec"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List running sandboxes",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		c := exec.Command("docker", "ps",
			"--filter", "label="+cmd.LabelSel,
			"--format", `table {{.Names}}\t{{.Status}}\t{{.Label "`+cmd.LabelWs+`"}}`)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	cmd.RootCmd.AddCommand(lsCmd)
}
