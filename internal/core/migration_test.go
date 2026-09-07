package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"testing"
)

func TestMigrationCore_RebuildsReceiptFromVerifiedArchive(t *testing.T) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	if _, err := gz.Write([]byte("verified core")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	a := supportedAsset{OS: "linux", Arch: "amd64", Tag: "v1.19.30", Channel: "stable", AssetSHA256: digest(raw.Bytes()), Size: int64(raw.Len())}
	binary, receipt, err := rebuildMigrationCore(context.Background(), a, raw.Bytes())
	var got ProvenanceReceipt
	if err != nil || string(binary) != "verified core" || json.Unmarshal(receipt, &got) != nil || got.AssetSHA256 != a.AssetSHA256 || got.BinarySHA256 != digest(binary) || got.Schema != provenanceSchema {
		t.Fatalf("missing rebuilt binary/receipt: binary=%q receipt=%+v err=%v", binary, got, err)
	}
	broken := append([]byte(nil), raw.Bytes()...)
	broken[len(broken)-1] ^= 1
	if _, _, err := rebuildMigrationCore(context.Background(), a, broken); err == nil {
		t.Fatal("unverified compressed asset authorized")
	}
}
