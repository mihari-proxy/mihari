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

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

const maxCatalogSize = 1 << 20

var profileIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func Defaults() Catalog {
	return Catalog{Schema: CatalogSchema, GlobalInterval: "12h", Profiles: []Profile{}}
}

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
		return Catalog{}, dataError("invalid subscription catalog")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Catalog{}, dataError("subscription catalog must contain one document")
	}
	if err := catalog.Normalize(); err != nil {
		return Catalog{}, err
	}
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
		if profile.Interval != "" {
			if _, err := parseInterval(profile.Interval); err != nil {
				return err
			}
		}
	}
	if index := c.Index(c.ActiveID); index < 0 || !c.Profiles[index].Enabled {
		c.ActiveID = ""
		for _, profile := range c.Profiles {
			if profile.Enabled && profile.Generation > 0 {
				c.ActiveID = profile.ID
				break
			}
		}
	}
	return nil
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
