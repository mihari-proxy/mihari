package preferences

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestService_UpdateConnectionsColumnsPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"host", "chain", "traffic"}
	got, err := service.Update(context.Background(), Update{ConnectionsColumns: want})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ConnectionsColumns, want) {
		t.Fatalf("columns=%v", got.ConnectionsColumns)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().ConnectionsColumns; !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened columns=%v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode=%#o", got)
		}
	}
}

func TestService_DefaultsDoNotAliasSnapshots(t *testing.T) {
	service, err := Open(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	first := service.Snapshot()
	if len(first.ConnectionsColumns) == 0 {
		t.Fatal("default columns must not be empty")
	}
	first.ConnectionsColumns[0] = "changed"
	if service.Snapshot().ConnectionsColumns[0] == "changed" {
		t.Fatal("snapshot aliases service state")
	}
}

func TestService_RejectsInvalidConnectionsColumns(t *testing.T) {
	service, err := Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, columns := range [][]string{nil, {"host", "host"}, {"unknown"}} {
		_, err := service.Update(context.Background(), Update{ConnectionsColumns: columns})
		if !errors.Is(err, ErrInvalidColumns) {
			t.Fatalf("columns=%v err=%v", columns, err)
		}
	}
}

func TestService_UpdateHonorsCanceledContext(t *testing.T) {
	service, err := Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Update(ctx, Update{ConnectionsColumns: []string{"host"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
