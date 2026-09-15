package command

import (
	"fmt"

	"github.com/lulaide/minirunc/internal/spec"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func (app *application) validateCommand() *cobra.Command {
	var bundleDir string
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate an OCI bundle configuration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			bundle, err := spec.LoadBundle(bundleDir)
			if err != nil {
				return operationFailed("load bundle", err)
			}
			if err := spec.ValidateSpec(bundle.Spec); err != nil {
				return operationFailed("validate configuration", err)
			}
			app.logger.Debug("bundle validation passed",
				zap.String("bundle", bundle.Dir),
				zap.String("rootfs", bundle.RootfsPath),
			)
			if _, err := fmt.Fprintf(command.OutOrStdout(), "baseline configuration checks passed\nbundle: %s\nrootfs: %s\n", bundle.Dir, bundle.RootfsPath); err != nil {
				return operationFailed("write command output", err)
			}
			return nil
		},
	}
	command.Flags().StringVarP(&bundleDir, "bundle", "b", ".", "OCI bundle directory")
	return command
}
