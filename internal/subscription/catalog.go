package subscription

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

const maxCatalogSize = 1 << 20

var profileIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func Defaults() Catalog {
	return Catalog{Schema: CatalogSchema, GlobalInterval: "12h", Profiles: []Profile{}}
}

// Catalog loading pipeline (Load) runs these stages after decode:
//
//	migrate       — forward schema migrations (no-op for current schema);
//	                extension point for future vN→vN+1 reshapes.
//	Normalize     — validates schema and fields, repairs invariants. Reports
//	                errors; does NOT fill defaults.
//	fillDefaults  — fills zero values with intended defaults. Pure filling,
//	                never validates. Runs last so cleared values get a default.
//
// Contract: any caller that Normalize()s an in-memory catalog (Load, Save,
// service.Mutate, service.CommitRefresh) MUST follow with fillDefaults().
// Add new "zero ≠ default" fields to fillDefaults; keep Normalize about
// validation and invariant repair only.
func Load(path string) (Catalog, error) {
	file, err := os.Open(path)
	if err != nil {
		return Catalog{}, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil {
		return Catalog{}, err
	} else if info.Size() > maxCatalogSize {
		return Catalog{}, dataError("subscription catalog is too large")
	}
	decoder := yaml.NewDecoder(io.LimitReader(file, maxCatalogSize+1))
	decoder.KnownFields(true)
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, dataError(decodeErrorMessage(err, "subscription catalog"))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Catalog{}, dataError("subscription catalog must contain one document")
	}
	catalog.migrate()
	if err := catalog.Normalize(); err != nil {
		return Catalog{}, err
	}
	catalog.fillDefaults()
	return catalog, nil
}

func LoadOrCreate(path string) (Catalog, error) {
	catalog, err := Load(path)
	if err == nil {
		return catalog, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Catalog{}, err
	}
	catalog = Defaults()
	if err := Save(path, catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Save(path string, catalog Catalog) error {
	if err := catalog.Normalize(); err != nil {
		return err
	}
	catalog.fillDefaults()
	content, err := yaml.Marshal(catalog)
	if err != nil {
		return dataError("encode subscription catalog")
	}
	return config.AtomicWrite(path, content, 0o600)
}

func (c *Catalog) Normalize() error {
	if c.Schema != CatalogSchema {
		return dataError("unsupported subscription catalog schema")
	}
	if _, err := parseInterval(c.GlobalInterval); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(c.Profiles))
	for i := range c.Profiles {
		profile := &c.Profiles[i]
		if !profileIDPattern.MatchString(profile.ID) {
			return dataError("invalid subscription ID")
		}
		if _, exists := seen[profile.ID]; exists {
			return dataError("duplicate subscription ID")
		}
		seen[profile.ID] = struct{}{}
		profile.Name = strings.TrimSpace(profile.Name)
		if profile.Name == "" {
			return dataError("subscription name is required")
		}
		parsed, err := url.Parse(profile.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return dataError("subscription URL must use HTTP or HTTPS")
		}
		if !ValidProxyMode(profile.ProxyMode) {
			return dataError("invalid subscription proxy mode")
		}
		if profile.Interval != "" {
			if _, err := parseInterval(profile.Interval); err != nil {
				return err
			}
		}
	}
	// ActiveID must point at an enabled profile with a fetched generation;
	// otherwise clear it so fillDefaults can re-pick. Pure repair — default
	// selection lives in fillDefaults, not here.
	if c.ActiveID != "" {
		if index := c.Index(c.ActiveID); index < 0 || !c.Profiles[index].Enabled || c.Profiles[index].Generation == 0 {
			c.ActiveID = ""
		}
	}
	return nil
}

// migrate applies forward schema migrations to a decoded catalog before
// Normalize validates it. The current schema needs no migration; this switch
// is the extension point for future vN→vN+1 field renames or reshapes that
// fillDefaults cannot express. Schema validity itself stays enforced by
// Normalize, so unknown schemas are left untouched here.
func (c *Catalog) migrate() {
	switch c.Schema {
	case CatalogSchema:
		// no migration needed
	}
}

// fillDefaults fills zero values with their intended defaults. It only fills,
// never validates — run it after Normalize so values Normalize cleared get a
// sensible default. Today it selects an active subscription when none is set;
// future "zero ≠ default" fields belong here rather than scattered in Normalize.
func (c *Catalog) fillDefaults() {
	if c.ActiveID != "" {
		return
	}
	for _, profile := range c.Profiles {
		if profile.Enabled && profile.Generation > 0 {
			c.ActiveID = profile.ID
			return
		}
	}
}

func (c Catalog) Index(id string) int {
	for index := range c.Profiles {
		if c.Profiles[index].ID == id {
			return index
		}
	}
	return -1
}

func (c Catalog) EffectiveInterval(profile Profile) time.Duration {
	value := profile.Interval
	if value == "" {
		value = c.GlobalInterval
	}
	duration, _ := time.ParseDuration(value)
	return duration
}

func parseInterval(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, dataError(fmt.Sprintf("invalid refresh interval %q", value))
	}
	return duration, nil
}

func dataError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}

// decodeErrorMessage turns a yaml decode error into a user-facing message.
// Unknown fields (surfaced as *yaml.TypeError with "not found in type") are
// called out explicitly so a downgrade mismatch or a typo is obvious instead
// of a generic "invalid" failure.
func decodeErrorMessage(err error, what string) string {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		for _, item := range te.Errors {
			if strings.Contains(item, "not found in type") {
				return fmt.Sprintf("invalid %s: unknown field — %s; it may have been written by a newer mihari version or be a typo", what, item)
			}
		}
	}
	return "invalid " + what
}
