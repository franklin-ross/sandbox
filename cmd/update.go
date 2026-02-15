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
	Long:  `Copy the workflow binary, agent binary, entrypoint script, and firewall script into a running sandbox container without rebuilding the image.`,
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
			{agentBinary, "/usr/local/bin/agent", "agent binary"},
			{entrypointScript, "/opt/entrypoint.sh", "entrypoint script"},
			{firewallScript, "/opt/init-firewall.sh", "firewall script"},
		}

		for _, f := range files {
			if err := copyToContainer(name, f.data, f.dest); err != nil {
				return fmt.Errorf("copy %s: %w", f.desc, err)
			}
			fmt.Printf("  pushed %s → %s\n", f.desc, f.dest)
		}

		if theme := zshTheme(); theme != "" {
			sedCmd := fmt.Sprintf(`s/^ZSH_THEME=.*/ZSH_THEME="%s"/`, theme)
			if err := exec.Command("docker", "exec", name, "sed", "-i", sedCmd, "/home/agent/.zshrc").Run(); err != nil {
				return fmt.Errorf("update ZSH_THEME: %w", err)
			}
			fmt.Printf("  applied ZSH_THEME=%s\n", theme)

			if tp := customThemePath(theme); tp != "" {
				data, err := os.ReadFile(tp)
				if err != nil {
					return fmt.Errorf("read custom theme: %w", err)
				}
				dest := fmt.Sprintf("/home/agent/.oh-my-zsh/custom/themes/%s.zsh-theme", theme)
				if err := copyToContainer(name, data, dest); err != nil {
					return fmt.Errorf("copy custom theme: %w", err)
				}
				if err := exec.Command("docker", "exec", "-u", "root", name, "chown", "agent:agent", dest).Run(); err != nil {
					return fmt.Errorf("chown custom theme: %w", err)
				}
				fmt.Printf("  pushed custom theme → %s\n", dest)
			}
		}

		fmt.Println("\nUpdate complete. Entrypoint & firewall changes take effect on next container restart.")
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
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		return err
	}
	tmp.Close()

	return exec.Command("docker", "cp", tmp.Name(), container+":"+dest).Run()
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
