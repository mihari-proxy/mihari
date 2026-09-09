package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func legacyResourceFixture(t *testing.T, frontier int, done, absent, states bool) (*memoryProviderFiles, map[string]string) {
	t.Helper()
	fs := newMemoryProviderFiles()
	ctx := context.Background()
	entries := []map[string]any{}
	wants := map[string]string{}
	roles := []string{"provider", "country-mmdb", "asn-mmdb", "geoip-dat", "geosite-dat", "configuration"}
	if states {
		roles = append(roles, "settings", "catalog", "source-cache", "onboarding")
	}
	for i, role := range roles {
		tx := fmt.Sprintf("%032x", i+1)
		e := map[string]any{"transaction": tx, "subscription_id": "", "generation": uint64(0), "kind": "", "name": "", "resource": "", "format": "", "geo": "", "configuration": false, "state_role": "",
			"backup_intent": false, "backup_done": false, "swap_intent": false, "swap_done": false, "restore_intent": false, "restore_done": false}
		path := ""
		switch role {
		case "provider":
			id, err := ProviderResourceID(legacySubscription, 7, "rule", "domains")
			if err != nil {
				t.Fatal(err)
			}
			path, err = providerResourcePath(id, "yaml")
			if err != nil {
				t.Fatal(err)
			}
			e["subscription_id"], e["generation"], e["kind"], e["name"], e["resource"], e["format"] = legacySubscription, uint64(7), "rule", "domains", id, "yaml"
		case "configuration":
			path = "runtime/config.yaml"
			e["configuration"] = true
		case "settings", "catalog", "source-cache", "onboarding":
			id := ""
			if role == "source-cache" {
				id = legacySubscription
			}
			var err error
			path, err = stateTarget(resourceStateRole(role), id)
			if err != nil {
				t.Fatal(err)
			}
			e["state_role"], e["subscription_id"] = role, id
		default:
			name, err := GeoResourcePath(GeoResourceKind(role))
			if err != nil {
				t.Fatal(err)
			}
			path = "runtime/core-home/" + name
			e["geo"] = role
		}
		old := providerObject{}
		oldBytes := ""
		if !absent {
			oldBytes = "old " + role
			old = seedLegacyObject(t, fs, path, oldBytes)
		}
		newBytes := "new " + role
		candidate := "staging/providers/" + tx + "/candidate"
		next := seedLegacyObject(t, fs, candidate, newBytes)
		marker := seedLegacyObject(t, fs, "staging/providers/"+tx+"/transaction-id", tx)
		e["old"], e["new"], e["marker"] = old, next, marker
		progress := frontier - i*6
		if done {
			progress = 6
		}
		if progress >= 1 {
			e["backup_intent"] = true
		}
		if progress >= 2 && old.Present {
			if err := fs.move(ctx, path, old, path+".old-"+tx, providerObject{}); err != nil {
				t.Fatal(err)
			}
		}
		if progress >= 3 {
			e["backup_done"] = true
		}
		if progress >= 4 {
			e["swap_intent"] = true
		}
		if progress >= 5 {
			if err := fs.move(ctx, candidate, next, path, providerObject{}); err != nil {
				t.Fatal(err)
			}
		}
		if progress >= 6 {
			e["swap_done"] = true
		}
		wants[path] = oldBytes
		if done {
			wants[path] = newBytes
		}
		entries = append(entries, e)
	}
	seedLegacyJSON(t, fs, resourceJournalPath, map[string]any{"schema": "mihari.resource-activation/v1", "done": done, "entries": entries})
	fs.mutations = 0
	return fs, wants
}
func TestLegacyResourceRecovery_WholeTupleEveryForwardAndRecoveryBoundary(t *testing.T) {
	for _, absent := range []bool{false, true} {
		for frontier := 0; frontier <= 60; frontier++ {
			t.Run(fmt.Sprintf("absent=%v/frontier=%d", absent, frontier), func(t *testing.T) {
				fs, wants := legacyResourceFixture(t, frontier, false, absent, true)
				exerciseLegacyRecovery(t, fs, wants)
			})
		}
	}
	for _, absent := range []bool{false, true} {
		fs, wants := legacyResourceFixture(t, 60, true, absent, true)
		exerciseLegacyRecovery(t, fs, wants)
	}
}
func TestLegacyResourceRecovery_PreStateRoleJournal(t *testing.T) {
	fs, wants := legacyResourceFixture(t, 36, false, false, false)
	fs.objects[resourceJournalPath] = []byte(strings.ReplaceAll(string(fs.objects[resourceJournalPath]), `"state_role":"",`, ""))
	exerciseLegacyRecovery(t, fs, wants)
}
func mutateLegacyResource(t *testing.T, fs *memoryProviderFiles, change func(map[string]any, []any)) {
	t.Helper()
	var j map[string]any
	if err := json.Unmarshal(fs.objects[resourceJournalPath], &j); err != nil {
		t.Fatal(err)
	}
	change(j, j["entries"].([]any))
	seedLegacyJSON(t, fs, resourceJournalPath, j)
}
func TestLegacyResourceRecovery_RejectsInvalidAuthority(t *testing.T) {
	cases := map[string]func(map[string]any, []any){
		"missing-config":   func(j map[string]any, e []any) { j["entries"] = append(e[:5], e[6:]...) },
		"duplicate-config": func(j map[string]any, e []any) { j["entries"] = append(e, e[5]) },
		"duplicate-role":   func(j map[string]any, e []any) { j["entries"] = append(e, e[6]) },
		"duplicate-tx": func(_ map[string]any, e []any) {
			e[1].(map[string]any)["transaction"] = e[0].(map[string]any)["transaction"]
		},
		"state-path":           func(_ map[string]any, e []any) { e[6].(map[string]any)["state_role"] = "../../control.token" },
		"state-extra-identity": func(_ map[string]any, e []any) { e[6].(map[string]any)["subscription_id"] = legacySubscription },
		"null-role":            func(_ map[string]any, e []any) { e[6].(map[string]any)["state_role"] = nil },
		"missing-field":        func(_ map[string]any, e []any) { delete(e[0].(map[string]any), "restore_done") },
		"aliased-object":       func(_ map[string]any, e []any) { e[0].(map[string]any)["new"] = e[0].(map[string]any)["old"] },
		"bad-action":           func(_ map[string]any, e []any) { e[0].(map[string]any)["backup_intent"] = false },
		"oversized": func(j map[string]any, e []any) {
			for len(e) <= 265 {
				e = append(e, e[0])
			}
			j["entries"] = e
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fs, _ := legacyResourceFixture(t, 60, false, false, true)
			before := cloneProviderMemory(fs)
			mutateLegacyResource(t, fs, change)
			if err := (&ProviderStore{files: fs}).Recover(context.Background()); err == nil {
				t.Fatal("invalid historical authority accepted")
			}
			for p, b := range before.objects {
				if p != resourceJournalPath && string(fs.objects[p]) != string(b) {
					t.Fatalf("invalid WAL modified %s", p)
				}
			}
		})
	}
}
func TestLegacyResourceRecovery_ReplacementPreserved(t *testing.T) {
	for _, target := range []string{"mihari.yaml", "runtime/config.yaml", "runtime/core-home/Country.mmdb", "staging/providers/00000000000000000000000000000001/transaction-id"} {
		fs, _ := legacyResourceFixture(t, 60, false, false, true)
		fs.objects[target] = []byte("foreign bytes")
		fs.identities[target] = "foreign identity"
		if err := (&ProviderStore{files: fs}).Recover(context.Background()); err == nil {
			t.Fatal("replacement accepted")
		}
		if string(fs.objects[target]) != "foreign bytes" {
			t.Fatal("replacement removed")
		}
	}
}
func TestLegacyResourceRecovery_GeoOnlyAllowed(t *testing.T) {
	fs, _ := legacyResourceFixture(t, 0, false, false, false)
	mutateLegacyResource(t, fs, func(j map[string]any, e []any) { j["entries"] = e[1:5] })
	if _, err := (&ProviderStore{files: fs}).loadResourceJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyResourceRecovery_MaximumFixedSet(t *testing.T) {
	fs, _ := legacyResourceFixture(t, 0, false, false, true)
	mutateLegacyResource(t, fs, func(j map[string]any, entries []any) {
		template := entries[0].(map[string]any)
		for i := 1; i < 256; i++ {
			e := map[string]any{}
			for k, v := range template {
				e[k] = v
			}
			tx := fmt.Sprintf("%032x", i+1000)
			name := fmt.Sprintf("legacy-provider-%d", i)
			id, err := ProviderResourceID(legacySubscription, 7, "rule", name)
			if err != nil {
				t.Fatal(err)
			}
			e["transaction"], e["name"], e["resource"], e["old"] = tx, name, id, providerObject{}
			e["new"] = seedLegacyObject(t, fs, "staging/providers/"+tx+"/candidate", "candidate")
			e["marker"] = seedLegacyObject(t, fs, "staging/providers/"+tx+"/transaction-id", tx)
			entries = append(entries, e)
		}
		j["entries"] = entries
	})
	journal, err := (&ProviderStore{files: fs}).loadResourceJournal(context.Background())
	if err != nil || len(journal.Entries) != 265 {
		t.Fatal("maximum historical fixed resource/state set rejected", err)
	}
}
