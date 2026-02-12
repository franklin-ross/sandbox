package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update [path]",
	Short: "Push updated files into a running sandbox",
	Long:  `Copy the workflow binary, entrypoint script, and firewall script into a running sandbox container without rebuilding the image.`,
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

		type file struct {
			data []byte
			dest string
			desc string
		}
		files := []file{
			{workflowBinary, "/usr/local/bin/workflow", "workflow binary"},
			{entrypointScript, "/opt/entrypoint.sh", "entrypoint script"},
			{firewallScript, "/opt/init-firewall.sh", "firewall script"},
		}

		for _, f := range files {
			if err := copyToContainer(name, f.data, f.dest); err != nil {
				return fmt.Errorf("copy %s: %w", f.desc, err)
			}
			fmt.Printf("  pushed %s → %s\n", f.desc, f.dest)
		}

		// Re-run the firewall script to apply iptables rules live.
		fmt.Println("  applying firewall rules...")
		out, err := exec.Command("docker", "exec", name, "sudo", "/opt/init-firewall.sh").CombinedOutput()
		if err != nil {
			return fmt.Errorf("apply firewall rules: %w\n%s", err, out)
		}

		fmt.Println("\nUpdate complete. Entrypoint changes take effect on next container restart.")
		return nil
	},
}

// copyToContainer writes data to a host temp file and docker-cp's it into the container.
func copyToContainer(container string, data []byte, dest string) error {
	tmp, err := os.CreateTemp("", "ao-sandbox-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := os.WriteFile(tmp.Name(), data, 0755); err != nil {
		return err
	}
	tmp.Close()

	return exec.Command("docker", "cp", tmp.Name(), container+":"+dest).Run()
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
