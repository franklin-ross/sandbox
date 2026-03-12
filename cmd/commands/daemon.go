package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var hostToolDaemonPort int

var hostToolDaemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Run the host tool daemon (internal)",
	Hidden: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cmd.RunHostToolDaemon(ctx, hostToolDaemonPort)
	},
}

func init() {
	hostToolDaemonCmd.Flags().IntVar(&hostToolDaemonPort, "port", cmd.DefaultHostToolPort, "TCP port to listen on")
	cmd.RootCmd.AddCommand(hostToolDaemonCmd)
}
