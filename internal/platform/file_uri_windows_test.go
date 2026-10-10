package platform

import "testing"

func TestFileURI_LocalhostUNCRoundTrip(t *testing.T) {
	path := `\\localhost\share\main.yaml`
	uri, err := FileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FileURIPath(uri)
	if err != nil || got != path {
		t.Fatalf("uri=%s path=%s err=%v", uri, got, err)
	}
}
