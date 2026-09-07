package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Reproduces the entrypoint sequence: resource/config publication followed by
// the separate business-state write, then process loss before durable Finish.
func TestResourceActivation_BusinessStateCrashRestoresCompleteTuple(t *testing.T) {
	fs, prepared, paths := activationFixture(t)
	ctx := context.Background()
	statePath := "mihari.yaml"
	if err := fs.write(ctx, statePath, []byte("old settings"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	if err := prepared.stageState(ctx, resourceSettings, "", []byte("new settings")); err != nil {
		t.Fatal(err)
	}
	activation, err := prepared.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = activation.PublishConfig(ctx, sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if err = activation.PublishState(ctx); err != nil {
		t.Fatal(err)
	}
	// A fresh store models startup: no live callback or receipt survives.
	if err = (&ProviderStore{files: fs}).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[paths[2]]) != "old config" || string(fs.objects[statePath]) != "old settings" {
		t.Fatal("startup recovered old config with new business state")
	}
}

type boundStateFiles struct {
	*memoryProviderFiles
	root string
}

func (f boundStateFiles) storeBinding() (string, string) { return f.root, "root-1" }

func TestResourceActivation_CatalogRejectsIndependentWritesUntilSettlement(t *testing.T) {
	fs, p, _ := activationFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	p.store.files = boundStateFiles{fs, root}
	id := "0123456789abcdef0123456789abcdef"
	before := Defaults()
	before.ActiveID = id
	before.Profiles = []Profile{{ID: id, Name: "active", URL: "https://example.test/sub", Enabled: true, Generation: 1, Version: 1}}
	path := filepath.Join(root, "subscriptions", "catalog.yaml")
	if err := Save(path, before); err != nil {
		t.Fatal(err)
	}
	service, err := Open(ServiceOptions{CatalogPath: path, CacheDir: filepath.Join(root, "subscriptions", "cache")})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(service.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, "subscriptions/catalog.yaml", raw, providerObject{}); err != nil {
		t.Fatal(err)
	}
	staged, err := service.StageUse(ctx, p, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = service.noteRefreshError(id, errors.New("concurrent download failed"))
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("catalog writer bypassed activation owner: %v", err)
	}
	if err = staged.Recheck(); err != nil {
		t.Fatal("read snapshot changed during activation")
	}
	a, err := p.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	staged.Cancel()
	if err = service.noteRefreshError(id, errors.New("after rollback")); err != nil {
		t.Fatal("catalog writer remained blocked after rollback")
	}
}

type doneWriteFailureFiles struct {
	*memoryProviderFiles
	after bool
}

func (f doneWriteFailureFiles) write(ctx context.Context, path string, b []byte, old providerObject) error {
	if path == resourceJournalPath && bytes.Contains(b, []byte(`"done":true`)) {
		if f.after {
			if err := f.memoryProviderFiles.write(ctx, path, b, old); err != nil {
				return err
			}
		}
		return errors.New("injected done write failure")
	}
	return f.memoryProviderFiles.write(ctx, path, b, old)
}

func TestResourceActivation_DoneWriteFailurePreservesActualAuthority(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			fs, p, paths, before, next := completeStateFixture(t, false)
			ctx := context.Background()
			p.store.files = doneWriteFailureFiles{fs, after}
			a, err := p.Activate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.PublishConfig(ctx, sha256.Sum256([]byte("new config"))); err != nil {
				t.Fatal(err)
			}
			if err = a.PublishState(ctx); err != nil {
				t.Fatal(err)
			}
			if err = a.Finish(ctx); err == nil || a.Committed() != after {
				t.Fatal("incorrect write outcome")
			}
			if !after {
				if err = a.Restore(ctx); err != nil {
					t.Fatalf("known pre-commit failure did not restore old tuple: %v", err)
				}
			}
			if err = (&ProviderStore{files: fs}).Recover(ctx); err != nil {
				t.Fatal(err)
			}
			want := before
			if after {
				want = next
			}
			for i, path := range paths {
				if string(fs.objects[path]) != want[i] {
					t.Fatal("mixed tuple after done write failure")
				}
			}
		})
	}
}

func TestResourceActivation_RecoversPreStateRoleJournal(t *testing.T) {
	fs, p, paths := activationFixture(t)
	ctx := context.Background()
	if _, err := p.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	var journal map[string]json.RawMessage
	if err := json.Unmarshal(fs.objects[resourceJournalPath], &journal); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(journal["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		delete(entry, "state_role")
	}
	var err error
	journal["entries"], err = json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs.objects[resourceJournalPath], err = json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&ProviderStore{files: fs}).Recover(ctx); err != nil {
		t.Fatalf("previous v1 resource WAL cannot recover: %v", err)
	}
	if string(fs.objects[paths[0]]) != "old" {
		t.Fatal("legacy rollback failed")
	}
}

type cleanupFailureFiles struct{ *memoryProviderFiles }

func (f cleanupFailureFiles) remove(ctx context.Context, path string, old providerObject) error {
	if f.resourceDoneDurable {
		return errors.New("injected cleanup failure")
	}
	return f.memoryProviderFiles.remove(ctx, path, old)
}

func TestResourceActivation_CatalogPublishesCommittedTupleDespiteCleanupError(t *testing.T) {
	fs, p, _ := activationFixture(t)
	ctx := context.Background()
	id := "0123456789abcdef0123456789abcdef"
	before := Defaults()
	before.ActiveID = id
	before.Profiles = []Profile{{ID: id, Name: "active", URL: "https://example.test/sub", Enabled: true, ProxyMode: ProxyModeDirect, Generation: 1, Version: 1}}
	service := &Service{catalogPath: filepath.FromSlash("/private/data/subscriptions/catalog.yaml"), cacheDir: filepath.FromSlash("/private/data/subscriptions/cache"), catalog: before, now: func() time.Time { return time.Unix(123, 0) }}
	b, err := yaml.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, "subscriptions/catalog.yaml", b, providerObject{}); err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, "subscriptions/cache/"+id+".yaml", []byte("old cache"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	p.store.files = cleanupFailureFiles{fs}
	staged, err := service.StageRefresh(ctx, p, PreparedRefresh{profileID: id, profileVersion: 1, result: FetchResult{Content: []byte("new cache")}}, id, 2)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.PublishConfig(ctx, sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if err = a.PublishState(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(service.Snapshot(), before) {
		t.Fatal("catalog memory published before durable done")
	}
	if err = a.Finish(ctx); err == nil || !a.Committed() {
		t.Fatalf("lost committed result: %v committed=%v", err, a.Committed())
	}
	committed := staged.Publish()
	if committed.Profiles[0].Generation != 2 || service.Snapshot().Profiles[0].Generation != 2 {
		t.Fatal("committed catalog not published")
	}
	if err = a.Restore(ctx); err == nil {
		t.Fatal("irreversible committed tuple was rolled back")
	}
	if err = (&ProviderStore{files: fs}).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var disk Catalog
	if err = yaml.Unmarshal(fs.objects["subscriptions/catalog.yaml"], &disk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(disk, service.Snapshot()) || string(fs.objects["subscriptions/cache/"+id+".yaml"]) != "new cache" || string(fs.objects["runtime/config.yaml"]) != "new config" {
		t.Fatal("disk and memory disagree after committed cleanup failure")
	}
}

func TestResourceActivation_SettingsRejectStaleCandidateAndDifferentRoot(t *testing.T) {
	fs, p, _ := activationFixture(t)
	ctx := context.Background()
	before := config.Defaults()
	before.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	after := before.Clone()
	after.Tun = map[string]any{"enable": true}
	newer := before.Clone()
	newer.WebAddr = "127.0.0.1:9998"
	b, err := yaml.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, "mihari.yaml", b, providerObject{}); err != nil {
		t.Fatal(err)
	}
	if err = p.StageSettings(ctx, filepath.FromSlash("/private/data/mihari.yaml"), before, after); err == nil {
		t.Fatal("stale settings resealed against newer disk")
	}
	if err = p.StageSettings(ctx, filepath.FromSlash("/other/data/mihari.yaml"), newer, after); err == nil {
		t.Fatal("another data root accepted")
	}
	if len(p.state) != 0 {
		t.Fatal("failed staging retained a state candidate")
	}
}

func TestResourceActivation_StateRolesRejectPathAuthority(t *testing.T) {
	for _, entry := range []resourceEntry{
		{StateRole: "../../control.token"}, {StateRole: resourceSettings, SubscriptionID: "0123456789abcdef0123456789abcdef"},
		{StateRole: resourceSourceCache, SubscriptionID: "../../control"}, {StateRole: resourceCatalog, Configuration: true}, {StateRole: resourceSettings, Geo: GeoSiteDAT},
	} {
		if _, err := entry.target(); err == nil {
			t.Fatal("invalid role acquired target authority")
		}
	}
}

func TestResourceActivation_StateIdentityReplacementRejectsActivation(t *testing.T) {
	for _, stateIndex := range []int{3, 4, 5, 6} {
		t.Run(fmt.Sprint(stateIndex), func(t *testing.T) {
			fs, p, paths, before, _ := completeStateFixture(t, false)
			ctx := context.Background()
			path := paths[stateIndex]
			old, err := fs.inspect(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if err = fs.write(ctx, path, fs.objects[path], old); err != nil {
				t.Fatal(err)
			}
			if _, err = p.Activate(ctx); err == nil {
				t.Fatal("same-byte replacement of sealed business object accepted")
			}
			for i, path := range paths {
				if string(fs.objects[path]) != before[i] {
					t.Fatal("identity conflict changed activation tuple")
				}
			}
		})
	}
}

func completeStateFixture(t *testing.T, absent bool) (*memoryProviderFiles, *PreparedResources, []string, []string, []string) {
	t.Helper()
	fs, prepared, paths := activationFixture(t)
	old := []string{"old", "old geo", "old config"}
	next := []string{"new", "new geo", "new config"}
	for _, role := range []resourceStateRole{resourceSettings, resourceCatalog, resourceSourceCache, resourceOnboarding} {
		id := ""
		if role == resourceSourceCache {
			id = "0123456789abcdef0123456789abcdef"
		}
		path, err := stateTarget(role, id)
		if err != nil {
			t.Fatal(err)
		}
		previous := "old " + string(role)
		if absent {
			previous = ""
		} else {
			if err = fs.write(context.Background(), path, []byte(previous), providerObject{}); err != nil {
				t.Fatal(err)
			}
		}
		after := "new " + string(role)
		if err = prepared.stageState(context.Background(), role, id, []byte(after)); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		old = append(old, previous)
		next = append(next, after)
	}
	fs.mutations = 0
	return fs, prepared, paths, old, next
}

func settleCompleteState(t *testing.T, p *PreparedResources, commit bool) {
	t.Helper()
	ctx := context.Background()
	a, err := p.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.PublishConfig(ctx, sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if err = a.PublishState(ctx); err != nil {
		t.Fatal(err)
	}
	if commit {
		err = a.Finish(ctx)
	} else {
		err = a.Restore(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestResourceActivation_BusinessStateEveryCrashBoundary(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, absent := range []bool{false, true} {
			fs, p, _, _, _ := completeStateFixture(t, absent)
			settleCompleteState(t, p, commit)
			boundaries := fs.mutations
			for point := 1; point <= boundaries; point++ {
				t.Run(fmt.Sprintf("commit=%v/absent=%v/point=%d", commit, absent, point), func(t *testing.T) {
					fs, p, paths, before, after := completeStateFixture(t, absent)
					fs.crashAt = point
					func() {
						defer func() {
							if got := recover(); got != "simulated crash" {
								t.Fatalf("crash=%v", got)
							}
						}()
						settleCompleteState(t, p, commit)
					}()
					fs.crashAt = 0
					// A reboot changes inode comparison rules; both modes must converge from
					// sealed hashes/markers without surviving service receipts.
					if point%2 == 0 {
						fs.boot = "reboot"
					}
					fresh := &ProviderStore{files: fs}
					for i := 0; i < 2; i++ {
						if err := fresh.Recover(context.Background()); err != nil {
							t.Fatal(err)
						}
					}
					want := before
					if fs.resourceDoneDurable {
						want = after
					}
					for i, path := range paths {
						if string(fs.objects[path]) != want[i] {
							t.Fatalf("mixed tuple at %s; done=%v", path, fs.resourceDoneDurable)
						}
					}
				})
			}
		}
	}
}
