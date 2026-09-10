package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUninstallFiles_RecognizedDataEntriesPass(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"mihari.yaml",
		".mihari.yaml.tmp-4294967295",
		"bin/mihomo",
		"runtime/config.yaml",
		"runtime/.mihari-abcdef123456",
		"runtime/core-home/Country.mmdb.old-0123456789abcdef0123456789abcdef",
		"runtime/core-home/providers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.yaml",
		"GeoIP.dat",
		"providers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.txt",
		"subscriptions/catalog.yaml",
		"subscriptions/cache/0123456789abcdef0123456789abcdef.yaml",
		"geoip/GeoLite2-Country.mmdb.previous",
		"preferences/tui.json",
		"web/active.json",
		"logs/mihari-daemon.log.2",
		"logs/mihomo.log.lock",
		"logs-export/mihari-logs-20260910-120304-+0800-2.zip",
		"staging/core/0123456789abcdef0123456789abcdef/candidate-binary",
		"staging/providers/0123456789abcdef0123456789abcdef/source",
		"locks/install-data-id",
		"install-control/state.json",
	} {
		writeUninstallFixture(t, root, name)
	}

	if err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckUninstallFiles_InventoriedScratchEntriesPass(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name  string
		isDir bool
	}{
		{name: "runtime/core-home/providers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.yaml.old-0123456789abcdef0123456789abcdef"},
		{name: "providers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.txt.old-0123456789abcdef0123456789abcdef"},
		{name: "subscriptions/cache/.0123456789abcdef0123456789abcdef.yaml.tmp-0"},
		{name: "staging/.mihomo-download-4294967295"},
		{name: "staging/.mihomo-candidate-1"},
		{name: "staging/geoip/.candidate-2.mmdb"},
		{name: "staging/panels/.zashboard-v1.2.3-3.zip"},
		{name: "staging/panels/zashboard-v1.2.3-4", isDir: true},
		{name: "staging/panels/metacubexd-build_1-5-previous", isDir: true},
	} {
		if test.isDir {
			writeUninstallDirFixture(t, root, test.name)
		} else {
			writeUninstallFixture(t, root, test.name)
		}
	}

	if err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckUninstallFiles_UnrecognizedScratchEntriesAreRejected(t *testing.T) {
	for _, test := range []struct {
		name  string
		isDir bool
	}{
		{name: ".mihari.yaml.tmp-personal-notes"},
		{name: "staging/.mihomo-download-important"},
		{name: "staging/geoip/.candidate-notes.mmdb"},
		{name: "staging/panels/zashboard--", isDir: true},
		{name: "staging/panels/.zashboard-v1-4294967296.zip"},
		{name: "runtime/core-home/providers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.old-0123456789abcdef0123456789abcdef.yaml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.isDir {
				writeUninstallDirFixture(t, root, test.name)
			} else {
				writeUninstallFixture(t, root, test.name)
			}

			err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}})
			var checkErr *UninstallFileError
			if !errors.As(err, &checkErr) || checkErr.RelativePath != test.name {
				t.Fatalf("error = %v, want %q refusal", err, test.name)
			}
		})
	}
}

func TestCheckUninstallFiles_RecognizedTargetKindsPass(t *testing.T) {
	base := t.TempDir()
	writeUninstallFixture(t, base, "data/mihari.yaml")
	writeUninstallFixture(t, base, "control.sock")
	writeUninstallFixture(t, base, ".mihari-endpoint-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.lock")
	writeUninstallFixture(t, base, "install-control/operation.lock")

	logs := t.TempDir()
	writeUninstallFixture(t, logs, "logs/mihari-tui.log.1")
	writeUninstallFixture(t, logs, "logs-export/mihari-logs-20260910-120304--0700.zip")

	program := t.TempDir()
	writeUninstallFixture(t, program, "mihari")
	writeUninstallFixture(t, program, ".mihari-binary.lock")

	control := t.TempDir()
	writeUninstallFixture(t, control, "startup.lock")

	err := CheckUninstallFiles(context.Background(), []UninstallTarget{
		{Path: base, Kind: "base"},
		{Path: logs, Kind: "logs"},
		{Path: program, Kind: "program"},
		{Path: control, Kind: "control"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckUninstallFiles_RecognizesExistingUnixServiceJournalArtifacts(t *testing.T) {
	root := t.TempDir()
	id := "0123456789abcdef0123456789abcdef"
	for _, name := range []string{
		"install-transaction.json",
		"transactions/" + id + "/transaction-id",
		"transactions/" + id + "/unit",
		"transactions/" + id + "/unit-bootstrap",
		"transactions/" + id + "/ready.json",
		"transactions/" + id + "/validation-launch.json",
	} {
		writeUninstallFixture(t, root, name)
	}

	if err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "base"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckUninstallFiles_UnknownEntryIsRejectedWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeUninstallFixture(t, root, "mihari.yaml")
	unknown := filepath.Join(root, "unknown.txt")
	const contents = "leave this alone"
	if err := os.WriteFile(unknown, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}})
	var checkErr *UninstallFileError
	if !errors.As(err, &checkErr) || checkErr.RelativePath != "unknown.txt" {
		t.Fatalf("error = %v, want unknown.txt refusal", err)
	}
	got, readErr := os.ReadFile(unknown)
	if readErr != nil || string(got) != contents {
		t.Fatalf("unknown entry changed: contents=%q error=%v", got, readErr)
	}
}

func TestCheckUninstallFiles_SymlinkIsRejected(t *testing.T) {
	root := t.TempDir()
	writeUninstallFixture(t, root, "mihari.yaml")
	if err := os.Symlink(filepath.Join(root, "mihari.yaml"), filepath.Join(root, "daemon.lock")); err != nil {
		t.Fatal(err)
	}

	err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}})
	var checkErr *UninstallFileError
	if !errors.As(err, &checkErr) || checkErr.RelativePath != "daemon.lock" {
		t.Fatalf("error = %v, want daemon.lock link refusal", err)
	}
}

func TestCheckUninstallFiles_AbsentRootIsSkipped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if err := CheckUninstallFiles(context.Background(), []UninstallTarget{{Path: root, Kind: "data"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckUninstallFiles_CancellationIsReturned(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := CheckUninstallFiles(ctx, []UninstallTarget{{Path: t.TempDir(), Kind: "data"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func writeUninstallFixture(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeUninstallDirFixture(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
		t.Fatal(err)
	}
}
