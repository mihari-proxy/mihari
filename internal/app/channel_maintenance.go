package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

type channelMaintenance interface {
	Journal(context.Context) (*InstallJournal, error)
	Write(context.Context, string) error
	Close() error
}

func maintainChannel(ctx context.Context, layout platform.ResolvedLayout, uid uint32, value string, open func(context.Context) (channelMaintenance, error)) (err error) {
	if value != "main" && value != "dev" {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid release channel"}
	}
	if layout.Mode != platform.PrivateMode && (layout.Mode != platform.SystemMode || uid != 0) {
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "channel maintenance requires root"}
	}
	store, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	journal, err := store.Journal(ctx)
	if err != nil {
		return err
	}
	if journal != nil && journal.Phase != InstallPhaseComplete {
		return installBusy("install recovery required")
	}
	return store.Write(ctx, value)
}

func relevantChannelJournal(ctx context.Context, layout platform.ResolvedLayout, uid uint32, local *InstallJournal, readSystem func(context.Context) (*InstallJournal, error)) (*InstallJournal, error) {
	if local != nil && local.Phase != InstallPhaseComplete {
		return local, nil
	}
	if layout.Mode != platform.PrivateMode || uid != 0 {
		return local, nil
	}
	system, err := readSystem(ctx)
	if err != nil {
		return nil, err
	}
	if system != nil && system.Mode == string(platform.PrivateMode) && (system.DataRoot == layout.Data.Root || system.TargetPath == layout.Data.Root) {
		return system, nil
	}
	return local, nil
}
