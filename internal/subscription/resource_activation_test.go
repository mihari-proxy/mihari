package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func activationFixture(t *testing.T) (*memoryProviderFiles, *PreparedResources, []string) {
	t.Helper()
	fs, provider, target := seededProvider(t)
	ctx := context.Background()
	geoTarget := "runtime/core-home/GeoSite.dat"
	if err := fs.write(ctx, geoTarget, []byte("old geo"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	geo, err := provider.store.prepareGeo(ctx, GeoResourceSpec{Kind: GeoSiteDAT, Bytes: []byte("new geo"), SHA256: providerDigest([]byte("new geo"))})
	if err != nil {
		t.Fatal(err)
	}
	configTarget := "runtime/config.yaml"
	if err = fs.write(ctx, configTarget, []byte("old config"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	configuration, err := provider.store.prepareBytes(ctx, ProviderSpec{}, "", configTarget, []byte("new config"))
	if err != nil {
		t.Fatal(err)
	}
	configuration.configuration = true
	p := &PreparedResources{store: provider.store, providers: []*PreparedProvider{provider}, geo: []*PreparedProvider{geo}, configuration: configuration}
	fs.mutations = 0
	return fs, p, []string{target, geoTarget, configTarget}
}

func TestResourceActivation_ConfigBelongsToWholeRollback(t *testing.T) {
	fs, p, paths := activationFixture(t)
	ctx := context.Background()
	a, err := p.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(fs.objects["runtime/config.yaml"]) != "old config" {
		t.Fatal("configuration published before validation")
	}
	if err = a.PublishConfig(ctx, sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects["runtime/config.yaml"]) != "new config" {
		t.Fatal("validated config not published")
	}
	if err = a.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects["runtime/config.yaml"]) != "old config" || string(fs.objects[paths[0]]) != "old" || string(fs.objects[paths[1]]) != "old geo" {
		t.Fatal("whole config/resource rollback failed")
	}
}

func TestResourceActivation_EveryCrashRestoresWholeSet(t *testing.T) {
	fs, p, _ := activationFixture(t)
	a, err := p.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.PublishConfig(context.Background(), sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if err = a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	boundaries := fs.mutations
	for point := 1; point <= boundaries; point++ {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			fs, p, paths := activationFixture(t)
			fs.crashAt = point
			func() {
				defer func() {
					if got := recover(); got != "simulated crash" {
						t.Fatalf("expected crash, got %v", got)
					}
				}()
				a, err := p.Activate(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if err = a.PublishConfig(context.Background(), sha256.Sum256([]byte("new config"))); err != nil {
					t.Fatal(err)
				}
				if err = a.Restore(context.Background()); err != nil {
					t.Fatal(err)
				}
			}()
			fs.crashAt = 0
			s := &ProviderStore{files: fs}
			for i := 0; i < 2; i++ {
				if err := s.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if string(fs.objects[paths[0]]) != "old" || string(fs.objects[paths[1]]) != "old geo" || string(fs.objects[paths[2]]) != "old config" {
				t.Fatal("crash left mixed activation")
			}
		})
	}
}

func TestResourceActivation_EveryCommitCrashUsesDurableAuthority(t *testing.T) {
	fs, prepared, _ := activationFixture(t)
	activation, err := prepared.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = activation.PublishConfig(context.Background(), sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if err = activation.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	boundaries := fs.mutations
	for point := 1; point <= boundaries; point++ {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			fs, prepared, paths := activationFixture(t)
			fs.crashAt = point
			func() {
				defer func() {
					if got := recover(); got != "simulated crash" {
						t.Fatalf("expected crash, got %v", got)
					}
				}()
				activation, err := prepared.Activate(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if err = activation.PublishConfig(context.Background(), sha256.Sum256([]byte("new config"))); err != nil {
					t.Fatal(err)
				}
				if err = activation.Finish(context.Background()); err != nil {
					t.Fatal(err)
				}
			}()
			fs.crashAt = 0
			store := &ProviderStore{files: fs}
			for i := 0; i < 2; i++ {
				if err := store.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			want := []string{"old", "old geo", "old config"}
			if fs.resourceDoneDurable {
				want = []string{"new", "new geo", "new config"}
			}
			for i := range paths {
				if string(fs.objects[paths[i]]) != want[i] {
					t.Fatalf("path %s = %q, want %q (done=%v)", paths[i], fs.objects[paths[i]], want[i], fs.resourceDoneDurable)
				}
			}
		})
	}
}

func TestResourceActivation_JournalAllowsCompleteFixedSet(t *testing.T) {
	fs := newMemoryProviderFiles()
	journal := resourceJournal{Schema: "mihari.resource-activation/v1"}
	add := func(entry resourceEntry) {
		index := len(journal.Entries) + 1
		entry.Transaction = fmt.Sprintf("%032x", index)
		entry.New = providerObject{Present: true, Identity: fmt.Sprintf("new-%d", index), SHA256: providerDigest([]byte(fmt.Sprintf("new-%d", index))), BootID: fs.boot}
		entry.Marker = providerObject{Present: true, Identity: fmt.Sprintf("marker-%d", index), SHA256: providerDigest([]byte(entry.Transaction)), BootID: fs.boot}
		journal.Entries = append(journal.Entries, entry)
	}
	for i := 0; i < 256; i++ {
		name := fmt.Sprintf("provider-%d", i)
		id, err := ProviderResourceID("0123456789abcdef0123456789abcdef", 7, "rule", name)
		if err != nil {
			t.Fatal(err)
		}
		add(resourceEntry{SubscriptionID: "0123456789abcdef0123456789abcdef", Generation: 7, Kind: "rule", Name: name, Resource: id, Format: "yaml"})
	}
	for _, kind := range []GeoResourceKind{GeoCountryMMDB, GeoASNMMDB, GeoIPDAT, GeoSiteDAT} {
		add(resourceEntry{Geo: kind})
	}
	add(resourceEntry{Configuration: true})
	b, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(context.Background(), resourceJournalPath, b, providerObject{}); err != nil {
		t.Fatal(err)
	}
	if _, err = (&ProviderStore{files: fs}).loadResourceJournal(context.Background()); err != nil {
		t.Fatalf("complete fixed set rejected: %v", err)
	}
}

func TestResourceActivation_JournalRequiresSingleConfiguration(t *testing.T) {
	fs, prepared, _ := activationFixture(t)
	activation, err := prepared.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	journal := activation.journal
	journal.Entries = journal.Entries[:len(journal.Entries)-1]
	b, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	fs.objects[resourceJournalPath] = b
	if _, err = prepared.store.loadResourceJournal(context.Background()); err == nil {
		t.Fatal("resource journal without fixed configuration accepted")
	}
}

func TestResourceActivation_CrashAfterConfigPublicationRestoresWholeSet(t *testing.T) {
	fs, p, paths := activationFixture(t)
	a, err := p.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.PublishConfig(context.Background(), sha256.Sum256([]byte("new config"))); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[paths[2]]) != "new config" {
		t.Fatal("configuration publication fixture did not reach the crash window")
	}

	store := &ProviderStore{files: fs}
	if err = store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[paths[0]]) != "old" || string(fs.objects[paths[1]]) != "old geo" || string(fs.objects[paths[2]]) != "old config" {
		t.Fatal("restart recovery left config and resources from different authorities")
	}
}

func TestResourceActivation_RejectsDifferentGeneratedHash(t *testing.T) {
	fs, p, paths := activationFixture(t)
	a, err := p.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.PublishConfig(context.Background(), sha256.Sum256([]byte("different config"))); err == nil {
		t.Fatal("different generated hash authorized configuration publication")
	}
	if string(fs.objects[paths[2]]) != "old config" {
		t.Fatal("hash mismatch changed configuration")
	}
	if err = a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestResourceActivation_RestoresWholeFixedSet(t *testing.T) {
	fs, p, paths := activationFixture(t)
	a, err := p.Activate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[paths[0]]) != "new" || string(fs.objects[paths[1]]) != "new geo" {
		t.Fatal("activation did not swap the complete resource set")
	}
	if err = a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[paths[0]]) != "old" || string(fs.objects[paths[1]]) != "old geo" {
		t.Fatal("rollback left mixed fixed resources")
	}
}
