package preferences

import (
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPerformance_DefaultPersistValidateAndOmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Performance; got.ObservationReuseSeconds != 3 || got.ControllerOwnerDetection {
		t.Fatalf("default=%+v", got)
	}
	wanted := PerformancePreferences{ControllerOwnerDetection: true, ObservationReuseSeconds: 0}
	if _, err := s.Update(t.Context(), Update{Performance: &wanted}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Performance != wanted {
		t.Fatal("performance did not persist")
	}
	for _, seconds := range []int{-1, 6} {
		invalid := wanted
		invalid.ObservationReuseSeconds = seconds
		if _, err := s.Update(t.Context(), Update{Performance: &invalid}); err == nil {
			t.Fatalf("accepted %d", seconds)
		}
	}
	defaults := PerformancePreferences{ObservationReuseSeconds: 3}
	if _, err := s.Update(t.Context(), Update{Performance: &defaults}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["performance"]; ok {
		t.Fatal("defaults must omit performance for downgrade")
	}
}

func TestPreferences_SyncFailureRestoresOldFileAndAllowsRetry(t *testing.T) {
	for _, existed := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "tui.json")
		service, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if existed {
			if _, err := service.Update(t.Context(), Update{LogLevels: []string{"error"}}); err != nil {
				t.Fatal(err)
			}
		}
		old := service.Snapshot()
		raw, _ := os.ReadFile(path)
		write := service.writeFile
		failure := errors.New("directory sync failed")
		calls := 0
		service.writeFile = func(path string, body []byte, mode os.FileMode) (config.CommitResult, error) {
			calls++
			result, err := write(path, body, mode)
			if calls == 1 && err == nil {
				result.Warning = failure
			}
			return result, err
		}
		if _, err := service.Update(t.Context(), Update{LogLevels: []string{"warn"}}); !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
		if !reflect.DeepEqual(old, service.Snapshot()) {
			t.Fatal("failed save published new memory")
		}
		after, err := os.ReadFile(path)
		if existed {
			if err != nil || string(after) != string(raw) {
				t.Fatalf("old file not restored: err=%v body=%s", err, after)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed first save left new file")
		}
		if _, err := service.Update(t.Context(), Update{LogLevels: []string{"warn"}}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(service.Snapshot().LogLevels, []string{"warn"}) {
			t.Fatal("manual retry did not publish")
		}
	}
}

func TestPreferences_RestoreFailurePreservesBothCausesAndDraftCanRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(t.Context(), Update{LogLevels: []string{"error"}}); err != nil {
		t.Fatal(err)
	}
	previous := s.Snapshot()
	write := s.writeFile
	syncFailure, restoreFailure := errors.New("sync failed"), errors.New("restore failed")
	calls := 0
	s.writeFile = func(path string, body []byte, mode os.FileMode) (config.CommitResult, error) {
		calls++
		if calls == 2 {
			return config.CommitResult{}, restoreFailure
		}
		result, err := write(path, body, mode)
		result.Warning = syncFailure
		return result, err
	}
	if _, err = s.Update(t.Context(), Update{LogLevels: []string{"warn"}}); !errors.Is(err, syncFailure) || !errors.Is(err, restoreFailure) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(previous, s.Snapshot()) {
		t.Fatal("failed restore published new memory")
	}
	s.writeFile = write
	if _, err = s.Update(t.Context(), Update{LogLevels: []string{"warn"}}); err != nil {
		t.Fatal(err)
	}
}
