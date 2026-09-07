package service

import "testing"

func TestDefinitionFileState_IncludesPermissionSnapshot(t *testing.T) {
	original := DefinitionFile{Bytes: []byte("same unit"), Owner: 0, Mode: 0600}
	target := original
	target.Mode = 0644
	if DefinitionFileState(original, "") == DefinitionFileState(target, "") {
		t.Fatal("different permission states collapsed into one content hash")
	}
}
