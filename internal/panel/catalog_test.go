package panel

import "testing"

func TestBuiltInCatalogListsSupportedPanels(t *testing.T) {
	catalog := BuiltInCatalog()
	if len(catalog) != 2 {
		t.Fatalf("len=%d want 2", len(catalog))
	}
	byID := map[string]Entry{}
	for _, entry := range catalog {
		byID[entry.ID] = entry
	}
	zashboard, ok := byID[IDZashboard]
	if !ok || zashboard.Name != "Zashboard" {
		t.Fatalf("zashboard=%#v ok=%v", zashboard, ok)
	}
	metacubexd, ok := byID[IDMetaCubeXD]
	if !ok || metacubexd.Name != "MetaCubeXD" {
		t.Fatalf("metacubexd=%#v ok=%v", metacubexd, ok)
	}
}

func TestCatalogLookup(t *testing.T) {
	entry, ok := Lookup(IDZashboard)
	if !ok || entry.ID != IDZashboard {
		t.Fatalf("entry=%#v ok=%v", entry, ok)
	}
	if _, ok := Lookup("unknown"); ok {
		t.Fatal("expected unknown panel to be absent")
	}
}
