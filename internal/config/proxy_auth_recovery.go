package config

import (
	"bytes"
	"errors"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
)

// LoadForProxyAuthReset validates every setting except the auth block being removed.
// It does not write; only the daemon recovery owner may persist the returned candidate.
func LoadForProxyAuthReset(path string) (Settings, error) {
	file, err := os.Open(path)
	if err != nil {
		return Settings{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsSize+1))
	if err != nil {
		return Settings{}, err
	}
	if len(data) > maxSettingsSize {
		return Settings{}, dataError("settings file is too large")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return Settings{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Settings{}, dataError("settings file must contain one document", err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return Settings{}, dataError("invalid settings mapping")
	}
	mapping := node.Content[0]
	entries := []*yaml.Node{}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "proxy-auth" {
			continue
		}
		entries = append(entries, mapping.Content[i], mapping.Content[i+1])
	}
	mapping.Content = entries
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		return Settings{}, err
	}
	strict := yaml.NewDecoder(bytes.NewReader(encoded))
	strict.KnownFields(true)
	var settings Settings
	if err := strict.Decode(&settings); err != nil {
		return Settings{}, err
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	if settings.ControllerSecret == "" {
		return Settings{}, dataError("controller secret is required")
	}
	return settings, nil
}
