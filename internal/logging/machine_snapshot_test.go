package logging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMachineSnapshot_FixedPrefixesWindowAndExactStats(t *testing.T) {
	fs, paths := openExportTestFS(t)
	stamp := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	writeSnapshotFixture(t, fs, paths.DaemonLog, "{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"old-secret-value\"}\ninvalid\n{\"time\":\"2026-09-06T00:00:00Z\"}\n")
	source := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: NewRedactor("old-secret-value")})
	set, err := source.Open(context.Background(), SnapshotWindow{To: stamp})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	// Appends after Open are outside the captured file prefixes.
	file, err := fs.OpenAppend(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"late\"}\n")
	if closeErr := file.Close(); writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v %v", writeErr, closeErr)
	}
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Finish(context.Background()); err == nil {
		t.Fatal("Finish succeeded before EOF")
	}
	payload, changed, err := reader.Next(context.Background())
	if err != nil || !changed || strings.Contains(string(payload), "old-secret-value") {
		t.Fatalf("bad payload/redaction: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded["msg"] != "***" {
		t.Fatal("record not redacted")
	}
	digest := sha256.Sum256(append(append([]byte(nil), payload...), '\n'))
	if _, _, err := reader.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("want prefix EOF, got %v", err)
	}
	stats, err := reader.Finish(context.Background())
	if err != nil || stats.Lines != 1 || stats.Redacted != 1 || stats.SkippedInvalid != 1 || stats.Bytes != int64(len(payload)+1) || stats.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("wrong stats: %+v %v", stats, err)
	}
	if len(stats.Files) != 1 || stats.Files[0] != "mihari-daemon.log" {
		t.Fatalf("wrong files: %v", stats.Files)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	empty, err := set.Source(MihomoSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := empty.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("empty source: %v", err)
	}
	stats, err = empty.Finish(context.Background())
	if err != nil || stats.Lines != 0 || stats.Bytes != 0 || stats.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty stats: %+v %v", stats, err)
	}
	if err := set.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMachineSnapshot_TruncationCannotComplete(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.DaemonLog, "{\"time\":\"2026-09-05T00:00:00Z\"}\n")
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths}).Open(context.Background(), SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	file, err := os.OpenFile(paths.DaemonLog, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	truncateErr := file.Truncate(0)
	if closeErr := file.Close(); truncateErr != nil || closeErr != nil {
		t.Fatalf("truncate: %v %v", truncateErr, closeErr)
	}
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Next(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated prefix accepted: %v", err)
	}
	if _, err := reader.Finish(context.Background()); err == nil {
		t.Fatal("truncated source completed")
	}
	if err := set.Finish(context.Background()); err == nil {
		t.Fatal("truncated set completed")
	}
}

func TestMachineSnapshot_RetainsInitialAndNewSecrets(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.DaemonLog, "{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"initial-secret later-secret\"}\n{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"initial-secret later-secret newest-secret\"}\n")
	redactor := NewRedactor("initial-secret")
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: redactor}).Open(context.Background(), SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"later-secret", "newest-secret"} {
		redactor.ReplaceExact([]string{next})
		payload, changed, err := reader.Next(context.Background())
		if err != nil || !changed || strings.Contains(string(payload), "-secret") {
			t.Fatalf("secrets not retained: changed=%v err=%v", changed, err)
		}
	}
	if redactor.String("initial-secret") != "initial-secret" {
		t.Fatal("snapshot modified shared daemon redactor")
	}
}

func TestMachineSnapshot_RetainsSecretsReplacedBetweenReads(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.DaemonLog, "{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"temporary-secret\"}\n")
	redactor := NewRedactor("initial-secret")
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: redactor}).Open(context.Background(), SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	redactor.ReplaceExact([]string{"temporary-secret"})
	redactor.ReplaceExact([]string{"latest-secret"})
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	payload, changed, err := reader.Next(context.Background())
	if err != nil || !changed || strings.Contains(string(payload), "temporary-secret") {
		t.Fatalf("secret replaced between reads was not retained: changed=%v err=%v", changed, err)
	}
}

func TestRedactor_SnapshotReleaseStopsRetainingUpdates(t *testing.T) {
	source := NewRedactor("initial-secret")
	snapshot, release := source.snapshot()
	source.ReplaceExact([]string{"temporary-secret"})
	release()
	source.ReplaceExact([]string{"latest-secret"})
	if got := snapshot.String("initial-secret temporary-secret latest-secret"); got != "*** *** latest-secret" {
		t.Fatal("released snapshot still receives updates or lost its retained secrets")
	}
	next, releaseNext := source.snapshot()
	defer releaseNext()
	if got := next.String("initial-secret latest-secret"); got != "initial-secret ***" {
		t.Fatal("new snapshot inherited a prior snapshot's private secret history")
	}
}

func TestMachineSnapshot_SourceOrderAndEarlyClose(t *testing.T) {
	fs, paths := openExportTestFS(t)
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths}).Open(context.Background(), SnapshotWindow{To: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []SourceID{MihomoSource, TUISource, "../../mihari.yaml"} {
		if _, err := set.Source(id); err == nil {
			t.Fatalf("accepted source %q before daemon", id)
		}
	}
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Source(MihomoSource); err == nil {
		t.Fatal("accepted parallel source")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Finish(context.Background()); err == nil {
		t.Fatal("close substituted for EOF")
	}
	if err := set.Finish(context.Background()); err == nil {
		t.Fatal("incomplete set accepted")
	}
}

func TestMachineSnapshot_RecordOverflowFailsInsteadOfSkipping(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.DaemonLog, strings.Repeat("a", (1<<20)+1)+"\n")
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths}).Open(context.Background(), SnapshotWindow{To: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	reader, err := set.Source(DaemonSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Next(context.Background()); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("overflow treated as a successful empty source: %v", err)
	}
	if _, err := reader.Finish(context.Background()); err == nil {
		t.Fatal("oversized source completed")
	}
}
