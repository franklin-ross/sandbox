package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var providers = map[string]struct {
	envVar  string
	keyFile string
}{
	"anthropic": {envVar: "ANTHROPIC_API_KEY", keyFile: ".anthropic-key"},
}

var setKeyCmd = &cobra.Command{
	Use:   "set-key <provider>",
	Short: "Store an API key in the sandbox",
	Long:  `Store an API key for the given provider in the sandbox's persistent credential volume.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		p, ok := providers[provider]
		if !ok {
			return fmt.Errorf("unknown provider %q (supported: anthropic)", provider)
		}

		fmt.Printf("Enter %s: ", p.envVar)
		key, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read key: %w", err)
		}
		if len(key) == 0 {
			return fmt.Errorf("key cannot be empty")
		}

		name, err := ensureRunning(resolvePath("."))
		if err != nil {
			return err
		}

		dest := "/home/agent/.claude/" + p.keyFile
		writeCmd := exec.Command("docker", "exec", "-i", name, "sh", "-c",
			fmt.Sprintf("cat > %s", dest))
		writeCmd.Stdin = strings.NewReader(string(key))
		writeCmd.Stderr = os.Stderr
		if err := writeCmd.Run(); err != nil {
			return fmt.Errorf("write key to sandbox: %w", err)
		}

		// Regenerate .sandbox-env so the key is available immediately via docker exec --env-file.
		if err := regenerateEnvFile(name); err != nil {
			return fmt.Errorf("regenerate env file: %w", err)
		}

		fmt.Printf("Stored %s in sandbox.\n", p.envVar)
		return nil
	},
}

// regenerateEnvFile rebuilds /home/agent/.sandbox-env inside the container
// from the current key files. This is the same logic as entrypoint.sh so that
// keys set after startup take effect immediately for docker exec --env-file.
func regenerateEnvFile(container string) error {
	script := `
env_file=/home/agent/.sandbox-env
: > "$env_file"
if [ -f /home/agent/.claude/.anthropic-key ]; then
  echo "ANTHROPIC_API_KEY=$(cat /home/agent/.claude/.anthropic-key)" >> "$env_file"
fi
`
	cmd := exec.Command("docker", "exec", container, "sh", "-c", script)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func init() {
	rootCmd.AddCommand(setKeyCmd)
}
