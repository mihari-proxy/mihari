package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingSettings_PersistCloneAndDefaults(t *testing.T) {
	s := Defaults()
	s.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if s.RoutingMode() != "rule" || s.GlobalSelection("a") != "" {
		t.Fatal("incorrect defaults")
	}
	s.SetRoutingMode("global")
	s.SetGlobalSelection("a", "Hong Kong")
	s.SetGlobalSelection("b", "Auto")
	s.SetGlobalSelection("", "DIRECT")
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.RoutingMode() != "global" || got.GlobalSelection("a") != "Hong Kong" || got.GlobalSelection("b") != "Auto" || got.GlobalSelection("") != "DIRECT" {
		t.Fatal("routing intent did not round trip")
	}
	clone := got.Clone()
	clone.SetRoutingMode("direct")
	clone.SetGlobalSelection("a", "DIRECT")
	if got.RoutingMode() != "global" || got.GlobalSelection("a") != "Hong Kong" {
		t.Fatal("clone changed original routing intent")
	}
}

func TestRouting_OversizedSelectionsCannotReplaceLoadableSettings(t *testing.T) {
	s := Defaults()
	s.ControllerSecret = strings.Repeat("a", 64)
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		s.SetGlobalSelection(fmt.Sprintf("subscription-%d", i), strings.Repeat("x", 4096))
	}
	if _, err := SaveWithCommit(path, s); err == nil {
		t.Fatal("saved settings that Load cannot read")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("oversized save replaced last valid settings")
	}
}

func TestRoutingSettings_RejectInvalidMode(t *testing.T) {
	s := Defaults()
	s.SetRoutingMode("proxy")
	if s.Validate() == nil {
		t.Fatal("accepted invalid routing mode")
	}
}
