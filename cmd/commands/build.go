package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Force rebuild the sandbox image",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		fmt.Println("Building sandbox image...")
		if err := cmd.BuildImage(cmd.ImageHash()); err != nil {
			return err
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	cmd.RootCmd.AddCommand(buildCmd)
}
