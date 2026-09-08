package app

import "testing"

func TestServiceObjectIdentity_RecordedPublicationAndRestore(t *testing.T) {
	versions := []serviceObjectVersion{{Name: "original", State: "old", Identity: "1:2@3"}, {Name: "restore", State: "old", Identity: "1:4@3"}, {Name: "candidate", State: "new", Identity: "1:5@3"}}
	for _, v := range versions {
		if !serviceObjectMatches(versions, v.State, v.Identity, "boot", "boot") {
			t.Fatal("legitimate recorded object rejected", v.Name)
		}
	}
	if serviceObjectMatches(versions, "old", "1:99@3", "boot", "boot") {
		t.Fatal("equal bytes on foreign inode accepted")
	}
	if serviceObjectMatches(versions, "different", "1:2@3", "boot", "boot") {
		t.Fatal("changed content accepted")
	}
	if !serviceObjectMatches(versions, "old", "9:9@9", "boot", "next") {
		t.Fatal("private verified cross-boot content proof rejected")
	}
}
