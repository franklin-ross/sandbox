package commands

import (
	"fmt"
	"os"
	"path/filepath"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage sandbox configuration",
	Long:  `View and manage sandbox configuration files.`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize sandbox configuration",
	Long:  `Create the default sandbox configuration file and home directory.`,
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home directory: %w", err)
		}

		configPath := filepath.Join(home, ".sandbox", "config.yaml")
		homePath := filepath.Join(home, ".sandbox", "home")
		zshrcPath := filepath.Join(homePath, ".zshrc")

		configExists := fileExists(configPath)
		zshrcExists := fileExists(zshrcPath)

		if configExists && zshrcExists {
			fmt.Printf("Already exists: %s\n", configPath)
			fmt.Printf("Already exists: %s\n", zshrcPath)
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
		if err := os.MkdirAll(filepath.Join(homePath, "bin"), 0755); err != nil {
			return fmt.Errorf("create home directory: %w", err)
		}

		if configExists {
			fmt.Printf("Already exists: %s\n", configPath)
		} else {
			if err := os.WriteFile(configPath, []byte(cmd.DefaultConfigYAML), 0644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Printf("Created %s\n", configPath)
		}

		if zshrcExists {
			fmt.Printf("Already exists: %s\n", zshrcPath)
		} else {
			if err := os.WriteFile(zshrcPath, []byte(cmd.DefaultZshrc()), 0644); err != nil {
				return fmt.Errorf("write .zshrc: %w", err)
			}
			fmt.Printf("Created %s\n", zshrcPath)
		}
		return nil
	},
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func init() {
	configCmd.AddCommand(configInitCmd)
	cmd.RootCmd.AddCommand(configCmd)
}
