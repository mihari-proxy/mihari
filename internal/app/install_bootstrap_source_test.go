package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func bootstrapMigrationFixture(t *testing.T) *migrationFixture {
	t.Helper()
	fx := newMigrationFixture(t)
	entries, err := os.ReadDir(fx.source.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(fx.source.dir, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, fx.source.osPath("mihari-channel"), []byte("dev\n"))
	mustWrite(t, fx.source.osPath("install.lock"), nil)
	for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		mustWrite(t, fx.source.osPath("transactions/"+id+"/transaction-id"), []byte(id))
	}
	return fx
}

func TestUnixMigration_BootstrapResidue(t *testing.T) {
	fx := bootstrapMigrationFixture(t)
	before := fx.sourceHashes(t)
	prepared, err := prepareMigration(context.Background(), fx.options())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if len(prepared.settings) != 0 || prepared.activeID != "" {
		t.Fatal("bootstrap residue manufactured business state")
	}
	if err := prepared.recheckAndPublish(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.lock", "transactions", "mihari-channel"} {
		if _, err := os.Stat(fx.target.osPath(name)); !os.IsNotExist(err) {
			t.Fatalf("bootstrap metadata was published: %s (%v)", name, err)
		}
	}
	if !mapsEqual(before, fx.sourceHashes(t)) {
		t.Fatal("bootstrap source changed")
	}
}

func TestUnixMigration_BootstrapResidueMainChannel(t *testing.T) {
	fx := bootstrapMigrationFixture(t)
	mustWrite(t, fx.source.osPath("mihari-channel"), []byte(InstallChannelMain+"\n"))
	prepared, err := prepareMigration(context.Background(), fx.options())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if err := prepared.recheckAndPublish(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnixMigration_BootstrapResidueRejectsRecoveryState(t *testing.T) {
	for _, name := range []string{"journal", "backup", "marker-mismatch", "unknown-transaction", "business", "hardlink"} {
		t.Run(name, func(t *testing.T) {
			fx := bootstrapMigrationFixture(t)
			marker := "transactions/" + strings.Repeat("a", 32) + "/transaction-id"
			switch name {
			case "journal":
				mustWrite(t, fx.source.osPath("install-transaction.json"), []byte("pending"))
			case "backup":
				mustWrite(t, fx.source.osPath("transactions/"+strings.Repeat("a", 32)+"/unit-bootstrap"), []byte("recovery"))
			case "marker-mismatch":
				mustWrite(t, fx.source.osPath(marker), []byte(strings.Repeat("c", 32)))
			case "unknown-transaction":
				mustWrite(t, fx.source.osPath("transactions/unknown/transaction-id"), []byte("unknown"))
			case "business":
				mustWrite(t, fx.source.osPath("subscriptions/catalog.yaml"), []byte("preserve"))
			case "hardlink":
				fx.source.setFlags(marker, nodeFlags{hardlink: true, nlink: 2})
			}
			before := fx.sourceHashes(t)
			if prepared, err := prepareMigration(context.Background(), fx.options()); err == nil {
				prepared.cleanup()
				t.Fatal("unsafe bootstrap source accepted")
			}
			if !mapsEqual(before, fx.sourceHashes(t)) {
				t.Fatal("rejected source changed")
			}
		})
	}
}

func TestUnixMigration_BootstrapResidueRechecksBeforePublication(t *testing.T) {
	for _, phase := range []string{"after-copy", "after-stop"} {
		t.Run(phase, func(t *testing.T) {
			fx := bootstrapMigrationFixture(t)
			opts := fx.options()
			mutate := func() { mustWrite(t, fx.source.osPath("mihari.yaml"), fx.settingsYAML) }
			if phase == "after-copy" {
				opts.AfterCopy = mutate
			} else {
				opts.AfterStop = mutate
			}
			prepared, err := prepareMigration(context.Background(), opts)
			if err == nil {
				defer prepared.cleanup()
				err = prepared.recheckAndPublish(context.Background())
			}
			if err == nil || apiCode(err) != protocol.CodeRevisionConflict {
				t.Fatalf("changed bootstrap source: %v", err)
			}
		})
	}
}
