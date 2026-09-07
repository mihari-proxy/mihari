package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInstallTrust_OfflinePanelContainsVerifiedArchiveBytes(t *testing.T) {
	archive := []byte("fixture trusted panel ZIP bytes")
	hash := sha256HexBytes(archive)
	raw, err := json.Marshal(compiledInstallTrustFile{Panels: map[string]string{"zashboard/v1.2.3": hash}})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		calls := 0
		trust, err := decodeOfflineTrust(context.Background(), raw, func(_ context.Context, name string, limit int64) ([]byte, error) {
			calls++
			if name != hash+".zip" || limit <= 0 {
				t.Fatalf("unbound resource read: %q", name)
			}
			if valid {
				return archive, nil
			}
			return []byte("untrusted replacement"), nil
		})
		if valid {
			if err != nil || string(trust.panel["zashboard/v1.2.3"]) != string(archive) || calls != 1 {
				t.Fatalf("panel authority did not reconstruct actual bytes: calls=%d err=%v", calls, err)
			}
		} else if err == nil {
			t.Fatal("mismatched archive authorized")
		}
	}
}
