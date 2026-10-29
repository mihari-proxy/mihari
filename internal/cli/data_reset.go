package cli

import (
	"context"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
	"strconv"
)

type dataResetClient interface {
	DataResetPreview(context.Context) (protocol.ResetPreview, error)
	ResetBusinessData(context.Context, protocol.DataResetRequest) (protocol.DataResetResult, error)
}

func newDataResetCommand(deps Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "data-reset", Short: "Reset business data while retaining the installation, resources and logs"}
	for _, action := range []string{"preview", "run"} {
		var yes, confirm bool
		var digest string
		cmd := &cobra.Command{Use: action, Short: "Preview or confirm business data reset", Args: cobra.NoArgs}
		if action == "run" {
			cmd.Flags().BoolVar(&yes, "yes", false, "Confirm removal of listed business data")
			cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm again: stop proxy, reset settings and return to Setup")
			cmd.Flags().StringVar(&digest, "digest", "", "Digest obtained from data-reset preview")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			client, err := runtimeClient(deps)
			if err != nil {
				return err
			}
			api, ok := client.(dataResetClient)
			if !ok {
				return invalidArgument("daemon does not support business data reset")
			}
			if action == "run" && (!yes || !confirm || digest == "") {
				return invalidArgument("run data-reset preview, then supply --digest DIGEST --yes --confirm; this clears business data and stops proxying")
			}
			preview, err := api.DataResetPreview(cmd.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			if action == "preview" {
				if options.json {
					return renderJSON(cmd.OutOrStdout(), preview)
				}
				for _, p := range preview.Targets {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Reset:", strconv.Quote(p)); err != nil {
						return err
					}
				}
				for _, p := range preview.Preserved {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Keep:", p); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Digest:", preview.Digest)
				return err
			}
			if preview.Digest != digest {
				return invalidArgument("reset preview changed; review a new preview")
			}
			id, err := operationID(deps)
			if err != nil {
				return err
			}
			v, err := api.ResetBusinessData(cmd.Context(), protocol.DataResetRequest{OperationID: id, IfRevision: &preview.Revision, Digest: digest, Confirm: true, ConfirmAgain: true})
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(cmd.OutOrStdout(), v)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Business data reset. Restart the daemon, then complete Setup. Installation, resources, logs and local control credential were retained."); err != nil {
				return err
			}
			return renderWarnings(cmd.ErrOrStderr(), v.WarningOutcome)
		}
		root.AddCommand(cmd)
	}
	return root
}
