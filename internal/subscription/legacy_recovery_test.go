package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const legacySubscription = "0123456789abcdef0123456789abcdef"

func seedLegacyObject(t *testing.T, fs *memoryProviderFiles, path, content string) providerObject {
	t.Helper()
	if err := fs.write(context.Background(), path, []byte(content), providerObject{}); err != nil {
		t.Fatal(err)
	}
	object, err := fs.inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return object
}
func seedLegacyJSON(t *testing.T, fs *memoryProviderFiles, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fs.inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.write(context.Background(), path, content, object); err != nil {
		t.Fatal(err)
	}
}

// Each fixture is a historical durable filesystem state; it never invokes an
// active transaction writer or configuration policy to construct the WAL.
func legacyProviderFixture(t *testing.T, phase string, swapped, absent, copied bool) (*memoryProviderFiles, string) {
	t.Helper()
	fs := newMemoryProviderFiles()
	id, err := ProviderResourceID(legacySubscription, 7, "rule", "domains")
	if err != nil {
		t.Fatal(err)
	}
	path, err := providerResourcePath(id, "yaml")
	if err != nil {
		t.Fatal(err)
	}
	tx := "11111111111111111111111111111111"
	old := providerObject{}
	if !absent {
		old = seedLegacyObject(t, fs, path, "old")
	}
	candidate := "staging/providers/" + tx + "/candidate"
	next := seedLegacyObject(t, fs, candidate, "new")
	marker := seedLegacyObject(t, fs, "staging/providers/"+tx+"/transaction-id", tx)
	backup := providerObject{}
	if !absent && (copied || phase != "prepared") {
		backup = seedLegacyObject(t, fs, path+".old-"+tx, "old")
	}
	if phase == "prepared" {
		backup = providerObject{}
	}
	if swapped {
		fs.objects[path] = fs.objects[candidate]
		fs.identities[path] = fs.identities[candidate]
		delete(fs.objects, candidate)
		delete(fs.identities, candidate)
	}
	seedLegacyJSON(t, fs, providerJournalPath, map[string]any{
		"schema": "mihari.provider-commit/v1", "id": tx, "resource_id": id, "format": "yaml", "old": old, "new": next, "backup": backup, "phase": phase, "marker": marker,
		"subscription_id": legacySubscription, "generation": uint64(7), "kind": "rule", "name": "domains", "recovery_intent": false, "recovery_done": false,
	})
	fs.mutations = 0
	return fs, path
}
func assertLegacySettled(t *testing.T, fs *memoryProviderFiles, wants map[string]string) {
	t.Helper()
	for path, want := range wants {
		if string(fs.objects[path]) != want {
			t.Fatalf("wrong authority at %s", path)
		}
	}
	for path := range fs.objects {
		if strings.HasPrefix(path, "staging/providers/") || strings.Contains(path, ".old-") {
			t.Fatalf("legacy private object retained: %s", path)
		}
	}
}
func exerciseLegacyRecovery(t *testing.T, original *memoryProviderFiles, wants map[string]string) {
	t.Helper()
	ctx := context.Background()
	probe := cloneProviderMemory(original)
	if err := (&ProviderStore{files: probe}).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	assertLegacySettled(t, probe, wants)
	for _, reboot := range []bool{false, true} {
		for point := 0; point <= probe.mutations; point++ {
			fs := cloneProviderMemory(original)
			if reboot {
				fs.boot = "boot-2"
				for p, id := range fs.identities {
					fs.identities[p] = "reboot-" + id
				}
			}
			fs.crashAt = point
			crashProviderOperation(func() { _ = (&ProviderStore{files: fs}).Recover(ctx) })
			fs.crashAt = 0
			// A reboot in the middle of recovery must converge as well.
			if reboot {
				fs.boot = "boot-3"
				for p, id := range fs.identities {
					fs.identities[p] = "again-" + id
				}
			}
			for retry := 0; retry < 2; retry++ {
				if err := (&ProviderStore{files: fs}).Recover(ctx); err != nil {
					t.Fatalf("crash %d reboot %v: %v", point, reboot, err)
				}
			}
			assertLegacySettled(t, fs, wants)
		}
	}
	for _, after := range []bool{false, true} {
		for point := 1; point <= probe.mutations; point++ {
			fs := cloneProviderMemory(original)
			fault := &providerFaultFiles{providerFiles: fs, after: after, at: point}
			if err := (&ProviderStore{files: fault}).Recover(ctx); err == nil {
				t.Fatalf("IO failure %d after=%v swallowed", point, after)
			}
			if err := (&ProviderStore{files: fs}).Recover(ctx); err != nil {
				t.Fatal(err)
			}
			assertLegacySettled(t, fs, wants)
		}
	}
}
func TestLegacyProviderRecovery_EveryHistoricalStateAndRecoveryBoundary(t *testing.T) {
	for _, absent := range []bool{false, true} {
		for _, phase := range []string{"prepared", "intent", "done"} {
			for _, swapped := range []bool{false, true} {
				for _, copied := range []bool{false, true} {
					if phase == "prepared" && swapped || phase == "done" && !swapped {
						continue
					}
					t.Run(fmt.Sprintf("%s/absent=%v/swapped=%v/copied=%v", phase, absent, swapped, copied), func(t *testing.T) {
						fs, path := legacyProviderFixture(t, phase, swapped, absent, copied)
						want := "old"
						if absent {
							want = ""
						}
						if phase == "done" {
							want = "new"
						}
						exerciseLegacyRecovery(t, fs, map[string]string{path: want})
					})
				}
			}
		}
	}
}
func TestLegacyProviderRecovery_RejectsMalformedAndUnknownIdentity(t *testing.T) {
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"phase":"intent"`), []byte(`"phase":"intent","phase":"intent"`), 1)
		},
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema":`), []byte(`"Schema":`), 1) },
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"recovery_done":false`), []byte(`"recovery_done":null`), 1)
		},
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"recovery_done":false,`), nil, 1) },
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"boot_id":"boot-1"`), []byte(`"boot_id":""`), 1)
		},
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"identity":"1"`), []byte(`"identity":""`), 1) },
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"generation":7`), []byte(`"generation":0`), 1) },
	} {
		fs, path := legacyProviderFixture(t, "intent", true, false, true)
		before := string(fs.objects[path])
		fs.objects[providerJournalPath] = mutate(fs.objects[providerJournalPath])
		if err := (&ProviderStore{files: fs}).Recover(context.Background()); err == nil {
			t.Fatal("malformed journal accepted")
		}
		if string(fs.objects[path]) != before {
			t.Fatal("malformed journal changed target")
		}
	}
	for _, which := range []string{"target", "candidate", "backup", "marker"} {
		fs, path := legacyProviderFixture(t, "intent", false, false, true)
		target := map[string]string{"target": path, "candidate": "staging/providers/11111111111111111111111111111111/candidate", "backup": path + ".old-11111111111111111111111111111111", "marker": "staging/providers/11111111111111111111111111111111/transaction-id"}[which]
		fs.objects[target] = []byte("unrecognized replacement")
		fs.identities[target] = "foreign-object"
		if err := (&ProviderStore{files: fs}).Recover(context.Background()); err == nil {
			t.Fatalf("%s replacement accepted", which)
		}
		if string(fs.objects[target]) != "unrecognized replacement" {
			t.Fatalf("%s replacement removed", which)
		}
	}
}
func TestLegacyProviderRecovery_AbandonedCandidates(t *testing.T) {
	fs, _ := legacyProviderFixture(t, "prepared", false, false, false)
	delete(fs.objects, providerJournalPath)
	delete(fs.identities, providerJournalPath)
	if err := (&ProviderStore{files: fs}).Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for p := range fs.objects {
		if strings.HasPrefix(p, "staging/providers/") {
			t.Fatal("abandoned candidate retained")
		}
	}
}

func TestLegacyProviderIdentity_HistoricalHash(t *testing.T) {
	id, err := ProviderResourceID(legacySubscription, 7, "rule", "domains")
	if err != nil || id != "c47697e9688ec92af8714580d0721775e314c2444487be33586dd267f837089b" {
		t.Fatal("historical provider filename identity changed", err)
	}
}
