//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
	"os"
	"strings"
)

// QueryChannel reads the selected sidecar without service inspection or writes.
// Missing metadata retains the stable self-channel query default of main.
func (i *UnixInstaller) QueryChannel(ctx context.Context) (channel string, err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	raw, err := platform.ReadInstallChannel(ctx, i.layout)
	if errors.Is(err, os.ErrNotExist) {
		return update.ChannelMain, nil
	}
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value != update.ChannelMain && value != update.ChannelDev {
		return "", migrateData("invalid installed release channel")
	}
	return value, nil
}

// SetChannel owns one local metadata maintenance lease. It never acquires D/E,
// starts a service, or recovers an interrupted installation implicitly.
func (i *UnixInstaller) SetChannel(ctx context.Context, value string) (err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	return maintainChannel(ctx, i.layout, uint32(os.Geteuid()), value, func(ctx context.Context) (channelMaintenance, error) {
		lease, err := platform.AcquireChannelLease(ctx, i.layout)
		if err != nil {
			return nil, err
		}
		mode := uint32(0700)
		if i.layout.Mode == platform.SystemMode {
			mode = 0711
		}
		root, err := platform.OpenTrustedRoot(ctx, i.layout.BaseDir, platform.RootPolicy{Owner: uint32(os.Geteuid()), Mode: mode})
		if err != nil {
			return nil, errors.Join(err, lease.Close())
		}
		return &unixChannelMaintenance{root: root, lease: lease, layout: i.layout}, nil
	})
}

type unixChannelMaintenance struct {
	root   *platform.TrustedRoot
	lease  *platform.OwnedInstallLease
	layout platform.ResolvedLayout
}

func (s *unixChannelMaintenance) Close() error { return errors.Join(s.root.Close(), s.lease.Close()) }
func (s *unixChannelMaintenance) Journal(ctx context.Context) (journal *InstallJournal, err error) {
	local, err := readChannelJournal(ctx, s.root)
	if err != nil {
		return nil, err
	}
	return relevantChannelJournal(ctx, s.layout, uint32(os.Geteuid()), local, func(ctx context.Context) (journal *InstallJournal, err error) {
		// P is already locked. Read B without taking B after P; a same-P installer
		// must hold P before publishing its journal or progressing its transaction.
		root, err := platform.OpenTrustedRoot(ctx, platform.SystemLayoutDefaults().BaseDir, platform.RootPolicy{Owner: 0, Mode: 0711})
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, root.Close()) }()
		return readChannelJournal(ctx, root)
	})
}

func readChannelJournal(ctx context.Context, root *platform.TrustedRoot) (journal *InstallJournal, err error) {
	file, _, err := root.OpenFile(ctx, installJournalFileName, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	decoded, err := DecodeJournal(file)
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}
func (s *unixChannelMaintenance) Write(ctx context.Context, value string) error {
	if err := s.lease.Validate(ctx, s.layout, false); err != nil {
		return err
	}
	mode := uint32(0600)
	if s.layout.Mode == platform.SystemMode {
		mode = 0644
	}
	var expected *platform.FileIdentity
	file, id, err := s.root.OpenFile(ctx, "mihari-channel", mode)
	if err == nil {
		expected = &id
		if err = file.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// WriteFile reports post-rename sync errors without rolling back the visible
	// value. A retry re-observes the committed inode under this same stable lock.
	return s.root.WriteFile(ctx, "mihari-channel", []byte(value+"\n"), mode, expected)
}

// InstalledSourcePresent performs authoritative native service inspection for
// greenfield discovery. Unknown manager state never becomes an absent source.
func (i *UnixInstaller) InstalledSourcePresent(ctx context.Context) (bool, error) {
	def, err := i.adapter(nil).InspectDefinition(ctx)
	if err != nil {
		return false, err
	}
	if def.Status == service.StatusUnknown {
		return false, installBusy("service source is unknown")
	}
	return def.Status != service.StatusNotInstalled, nil
}
