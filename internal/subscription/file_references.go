package subscription

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// FileReference describes an input location and the path mihomo will read.
type FileReference struct {
	Location string `json:"location"`
	Path     string `json:"path"`
}

// FileReferences identifies native input references, excluding HTTP provider
// cache paths, inline TLS key pairs and SSH inline private keys.
func FileReferences(document Document) []FileReference {
	var refs []FileReference
	visitFileReferences(document, func(m map[string]any, key, location, value string) {
		refs = append(refs, FileReference{location, value})
	})
	sort.Slice(refs, func(i, j int) bool { return refs[i].Location < refs[j].Location })
	return refs
}

// ResolveFileReferences rebases parsed local YAML inputs. Raw cache bytes and
// source files are never modified, and remote sources pass an empty baseDir.
func ResolveFileReferences(document Document, baseDir string) {
	if baseDir == "" {
		return
	}
	visitFileReferences(document, func(m map[string]any, key, location, value string) {
		if !filepath.IsAbs(value) {
			m[key] = filepath.Join(baseDir, value)
		}
	})
}

func visitFileReferences(document Document, visit func(map[string]any, string, string, string)) {
	field := func(m map[string]any, key, location string) {
		if value, ok := m[key].(string); ok && value != "" {
			visit(m, key, location+"."+key, value)
		}
	}
	certificates := func(m map[string]any, location string) {
		cert, _ := m["certificate"].(string)
		key, _ := m["private-key"].(string)
		if cert != "" || key != "" {
			if _, err := tls.X509KeyPair([]byte(cert), []byte(key)); err != nil {
				field(m, "certificate", location)
				field(m, "private-key", location)
			}
		}
		if cert, ok := m["client-auth-cert"].(string); ok && cert != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(cert)) {
			field(m, "client-auth-cert", location)
		}
	}
	proxy := func(m map[string]any, location string) {
		if m["type"] == "ssh" {
			if key, _ := m["private-key"].(string); !strings.Contains(key, "PRIVATE KEY") {
				field(m, "private-key", location)
			}
		} else {
			certificates(m, location)
		}
	}
	for _, section := range []string{"proxy-providers", "rule-providers"} {
		if providers, ok := referenceMap(document[section]); ok {
			for name, value := range providers {
				if provider, ok := referenceMap(value); ok {
					location := section + "[" + name + "]"
					if provider["type"] == "file" {
						field(provider, "path", location)
					}
					if section == "proxy-providers" {
						if override, ok := referenceMap(provider["override"]); ok {
							certificates(override, location+".override")
						}
						if payload, ok := provider["payload"].([]any); ok {
							for i, item := range payload {
								if m, ok := referenceMap(item); ok {
									proxy(m, fmt.Sprintf("%s.payload[%d]", location, i))
								}
							}
						}
					}
				}
			}
		}
	}
	if m, ok := referenceMap(document["tls"]); ok {
		certificates(m, "tls")
	}
	for _, section := range []string{"proxies", "listeners"} {
		if list, ok := document[section].([]any); ok {
			for i, item := range list {
				if m, ok := referenceMap(item); ok {
					proxy(m, fmt.Sprintf("%s[%d]", section, i))
					if realm, ok := referenceMap(m["realm-opts"]); ok {
						certificates(realm, fmt.Sprintf("%s[%d].realm-opts", section, i))
					}
				}
			}
		}
	}
	for _, section := range []string{"tuic-server"} {
		if m, ok := referenceMap(document[section]); ok {
			certificates(m, section)
		}
	}
}

// FileReferenceWarning describes the consequences accepted on new creation.
const FileReferenceWarning = "This YAML references local files. Mihomo reads and may watch or update these files directly, using daemon/core permissions; changes can affect runtime even when subscription auto refresh is off. Moving, deleting or changing permissions can break runtime or restart. Mihari caches only the main YAML and cannot restore referenced files during rollback."

func referenceMap(value any) (map[string]any, bool) {
	switch m := value.(type) {
	case Document:
		return map[string]any(m), true
	case map[string]any:
		return m, true
	default:
		return nil, false
	}
}
