package app

import (
	"path/filepath"
	"testing"
)

func TestInstallationReset_OnlyFixedManagedEntriesAndRetainsCredential(t *testing.T) {
	root := t.TempDir()
	manifest := InstallationManifest{DataRoot: root, Credential: filepath.Join(root, "control.token")}
	keep, remove, err := installationDataEntries("fresh", manifest, nil, []string{"photos", "control.token", "logs", ".mihari-data.lock"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mihari.yaml", "onboarding.json", "subscriptions", "preferences", "runtime", "logs", "logs-export", "bin", "geoip", "web", "staging"}
	if len(remove) != len(want) {
		t.Fatalf("reset entries=%d", len(remove))
	}
	for i, name := range want {
		if remove[i].Path != filepath.Join(root, name) {
			t.Fatalf("reset entry %d not fixed", i)
		}
	}
	for _, name := range []string{"photos", "control.token", ".mihari-data.lock"} {
		found := false
		for _, entry := range keep {
			if entry.Path == filepath.Join(root, name) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing retained entry %s", name)
		}
	}
}

func TestInstallationRepair_DataPreviewNeverDeletes(t *testing.T) {
	root := t.TempDir()
	keep, remove, err := installationDataEntries("repair", InstallationManifest{DataRoot: root, Credential: filepath.Join(root, "control.token")}, nil, []string{"mihari.yaml", "photos"})
	if err != nil || remove == nil || len(remove) != 0 || len(keep) != 3 {
		t.Fatalf("repair preview keep=%d delete=%v err=%v", len(keep), remove, err)
	}
}

func TestInstallationReset_RejectsCredentialAndMigrationOverlap(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ credential, source string }{
		{filepath.Join(root, "web", "custom-token"), ""},
		{root, ""},
		{filepath.Join(root, "control.token"), root},
		{filepath.Join(root, "control.token"), filepath.Join(root, "old")},
		{filepath.Join(root, "control.token"), filepath.Dir(root)},
	} {
		var source *InstallationSourceScope
		if tc.source != "" {
			source = &InstallationSourceScope{DataRoot: tc.source}
		}
		_, _, err := installationDataEntries("fresh", InstallationManifest{DataRoot: root, Credential: tc.credential}, source, nil)
		if err == nil {
			t.Fatal("accepted overlapping protected scope")
		}
	}
}

func TestInstallationReset_RejectsSourceWithTargetIdentity(t *testing.T) {
	root := t.TempDir()
	identity := InstallationIdentity{BootID: "boot-a", Key: "same-root", Marker: "same-marker"}
	manifest := InstallationManifest{DataRoot: root, DataIdentity: &identity, Credential: filepath.Join(root, "control.token")}
	source := &InstallationSourceScope{DataRoot: filepath.Join(filepath.Dir(root), "lexical-alias"), DataIdentity: identity}
	if _, _, err := installationDataEntries(InstallationModeFresh, manifest, source, nil); err == nil {
		t.Fatal("accepted a migration source with the target root identity")
	}
}

func TestInstallationReset_RejectsNonChildObservation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".", "..", "../other", "logs/child", "logs\\child", "", "a\x00b"} {
		_, _, err := installationDataEntries("fresh", InstallationManifest{DataRoot: root, Credential: filepath.Join(root, "control.token")}, nil, []string{name})
		if err == nil {
			t.Fatal("accepted invalid direct-child observation")
		}
	}
}
