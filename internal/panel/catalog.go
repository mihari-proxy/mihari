package panel

// Built-in panel identifiers.
const (
	IDZashboard  = "zashboard"
	IDMetaCubeXD = "metacubexd"
)

// Entry is a catalog record for a supported Web panel adapter.
type Entry struct {
	ID   string
	Name string
}

// BuiltInCatalog returns the first-release panel adapters in stable order.
// Entries describe capability only; install state lives under web/{id}/{build}/ and active.json.
func BuiltInCatalog() []Entry {
	return []Entry{
		{ID: IDZashboard, Name: "Zashboard"},
		{ID: IDMetaCubeXD, Name: "MetaCubeXD"},
	}
}

// Lookup returns the catalog entry for id when the panel is supported.
func Lookup(id string) (Entry, bool) {
	for _, entry := range BuiltInCatalog() {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}
