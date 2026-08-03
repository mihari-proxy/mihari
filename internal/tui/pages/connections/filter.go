package connections

import (
	"slices"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const allSources = "All"

func matchesConnection(connection protocol.Connection, query, source string) bool {
	if source != "" && source != allSources && connection.Metadata.SourceIP != source {
		return false
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		connection.ID, connection.Rule, connection.RulePay, strings.Join(connection.Chains, " "),
		connection.Metadata.Network, connection.Metadata.Type,
		connection.Metadata.SourceIP, connection.Metadata.SourcePort,
		connection.Metadata.DestinationIP, connection.Metadata.DestinationPort,
		connection.Metadata.Host, connection.Metadata.Process, connection.Metadata.ProcessPath,
		connection.Metadata.InboundName, connection.Metadata.InboundUser,
		connection.Metadata.SniffHost, connection.Metadata.RemoteDestination,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func sourceOptions(connections []protocol.Connection) []string {
	seen := make(map[string]struct{})
	for _, connection := range connections {
		if connection.Metadata.SourceIP != "" {
			seen[connection.Metadata.SourceIP] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen)+1)
	result = append(result, allSources)
	for source := range seen {
		result = append(result, source)
	}
	slices.Sort(result[1:])
	return result
}

func validSource(source string, options []string) string {
	if slices.Contains(options, source) {
		return source
	}
	return allSources
}
