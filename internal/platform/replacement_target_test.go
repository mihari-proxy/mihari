package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacementFile_ContentAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari")
	if err := os.WriteFile(path, []byte("first"), 0700); err != nil {
		t.Fatal(err)
	}
	first, err := ObserveReplacementFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Exists || !filepath.IsAbs(first.Path) || first.FileID == "" || first.SHA256 != "a7937b64b8caa58f03721bb6bacf5c78cb235febe0e70b1b84cd99541461a08e" {
		t.Fatalf("invalid observation: %+v", first)
	}
	canonical, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(input, canonical) {
		t.Fatal("canonical path does not refer to the input entry")
	}
	if err := os.WriteFile(path, []byte("second"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := ObserveReplacementFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if first.FileID != second.FileID || first.SHA256 == second.SHA256 {
		t.Fatal("content change not bound to identity")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0700); err != nil {
		t.Fatal(err)
	}
	third, err := ObserveReplacementFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if third.FileID == second.FileID || third.SHA256 != second.SHA256 {
		t.Fatal("replacement identity not observed")
	}
}

func TestReplacementFile_AbsentAndInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "mihari")
	got, err := ObserveReplacementFile(context.Background(), path)
	if err != nil || got.Exists || got.Path != path || got.MayExecute {
		t.Fatalf("fresh: %+v %v", got, err)
	}
	if _, err := ObserveReplacementFile(context.Background(), "relative"); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("relative: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ObserveReplacementFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if _, err := ObserveReplacementFile(context.Background(), t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestReplacementFile_SizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = errors.Join(f.Truncate((128<<20)+1), f.Close())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveReplacementFile(context.Background(), path); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestReplacementFile_MissingUnderSymlinkParent(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "dangling"}[dangling], func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if !dangling {
				if err := os.MkdirAll(filepath.Join(target, "child"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			for _, tail := range []string{"mihari", filepath.Join("missing", "mihari"), filepath.Join("child", "mihari")} {
				got, err := ObserveReplacementFile(context.Background(), filepath.Join(link, tail))
				if err == nil {
					t.Fatalf("symlink ancestor accepted as fresh: %+v", got)
				}
			}
		})
	}
}

func TestReplacementFile_MissingUnderOrdinaryParent(t *testing.T) {
	root := t.TempDir()
	for _, tail := range []string{"mihari", filepath.Join("missing", "mihari")} {
		path := filepath.Join(root, tail)
		got, err := ObserveReplacementFile(context.Background(), path)
		if err != nil || got.Exists || got.MayExecute || got.Path != path {
			t.Fatalf("ordinary absent target: %+v %v", got, err)
		}
	}
}

func TestReplacementFile_HardlinksRetainDirectoryEntries(t *testing.T) {
	first := filepath.Join(t.TempDir(), "mihari")
	second := first + "-service"
	if err := os.WriteFile(first, []byte("installed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	a, err := ObserveReplacementFile(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ObserveReplacementFile(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if a.FileID != b.FileID || a.Path == b.Path {
		t.Fatal("different hardlink directory entries must retain separate paths")
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("installed"), 0700); err != nil {
		t.Fatal(err)
	}
	after, err := ObserveReplacementFile(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if after.FileID == b.FileID || after.SHA256 != b.SHA256 {
		t.Fatal("same-content replacement of one hardlink was not observed")
	}
	unchanged, err := ObserveReplacementFile(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.FileID != a.FileID {
		t.Fatal("observing one entry changed the other")
	}
}
