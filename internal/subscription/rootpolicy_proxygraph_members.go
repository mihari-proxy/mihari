package subscription

import (
	"context"
	"strings"
)

// knownProviderMembers models only proven names and matcher results. The full
// regexp2 expressions still go to the trusted core, including name replacement.
// Unknown is never interpreted as an edge that proves a configuration cycle.
func (g *policyProxyGraph) knownProviderMembers(ctx context.Context, provider preparedPolicyProvider, objects []int) ([]int, bool) {
	if !provider.ready {
		return nil, false
	}
	definition := provider.definition
	override, _ := definition.get("override")
	renames, _ := override.get("proxy-name")
	prefix, suffix := xhttpText(override, "additional-prefix"), xhttpText(override, "additional-suffix")
	filters := strings.Split(xhttpText(definition, "filter"), "`")
	// Multiple passes mutate the original mapping name. Avoid expanding that
	// native construction work or pretending its changing identities are known.
	if len(filters) > 1 && (prefix != "" || suffix != "" || len(renames.items) != 0) {
		return nil, false
	}
	seen := make(map[string]bool)
	var members []int
	for _, filter := range filters {
		for _, id := range objects {
			if ctx.Err() != nil {
				return nil, false
			}
			object := g.objects[id]
			if policyTypeExcluded(xhttpText(definition, "exclude-type"), object.kind) {
				continue
			}
			excluded := policyNameExclusion(xhttpText(definition, "exclude-filter"), object.name)
			match := policyNamePattern(filter, object.name)
			if excluded == policyMatchUnknown || match == policyMatchUnknown {
				return nil, false
			}
			if excluded == policyMatchYes || match == policyMatchNo || seen[object.name] {
				continue
			}
			seen[object.name] = true
			members = append(members, id)
		}
	}
	for _, id := range members {
		g.objects[id].name = prefix + g.objects[id].name + suffix
		g.objects[id].nameUnknown = len(renames.items) != 0
	}
	return members, true
}

func policyNameExclusion(filter, name string) policyMatch {
	if filter == "" {
		return policyMatchNo
	}
	return policyNameFilter(filter, name)
}

func policyTypeExcluded(list, kind string) bool {
	if list == "" {
		return false
	}
	for _, excluded := range strings.Split(list, "|") {
		if strings.EqualFold(excluded, kind) {
			return true
		}
	}
	return false
}

// These are AdapterType.String results, not source type discriminator values.
func policyAdapterTypeName(object policyProxyObject) string {
	switch object.kind {
	case "ss":
		return "Shadowsocks"
	case "ssr":
		return "ShadowsocksR"
	case "gost-relay":
		return "GostRelay"
	case "select":
		return "Selector"
	case "url-test":
		return "URLTest"
	case "load-balance":
		return "LoadBalance"
	case "REJECT-DROP":
		return "RejectDrop"
	case "PASS-RULE":
		return "PassRule"
	default:
		return object.kind // Other names differ only by case.
	}
}

func (g *policyProxyGraph) knownGroupMembers(ctx context.Context, group policyProxyObject) ([]int, bool) {
	filter := xhttpText(group.value, "filter")
	onlyLocal := !group.unknownMembers
	for _, member := range group.members {
		onlyLocal = onlyLocal && member.local
	}
	// Multi-filter sorting can reorder and deduplicate across provider objects.
	// A sole local Compatible provider bypasses positive filters altogether.
	// Other multi-filter ordering remains outside this sound static subset.
	if strings.Contains(filter, "`") && !onlyLocal {
		return nil, false
	}
	var members []int
	unknown := group.unknownMembers
	selectionUncertain := false
	for _, member := range group.members {
		if ctx.Err() != nil {
			return nil, false
		}
		// Known local objects precede any uncertain provider membership. Other
		// provider names could be shadowed by an unknown earlier object.
		if group.unknownMembers && !member.local {
			continue
		}
		object := g.objects[member.object]
		if policyTypeExcluded(xhttpText(group.value, "exclude-type"), policyAdapterTypeName(object)) {
			continue
		}
		if !member.local && filter != "" {
			match := policyObjectNameFilter(filter, object)
			if match == policyMatchUnknown {
				unknown = true
				selectionUncertain = true
				continue
			}
			if match == policyMatchNo {
				continue
			}
		}
		exclusion := policyMatchNo
		if filter := xhttpText(group.value, "exclude-filter"); filter != "" {
			exclusion = policyObjectNameFilter(filter, object)
		}
		switch exclusion {
		case policyMatchUnknown:
			unknown = true
			selectionUncertain = true
			continue
		case policyMatchYes:
			continue
		}
		// An earlier unresolved candidate may survive under this same name.
		// Dropping that candidate must not turn a shadowed later object into a
		// supposedly selectable cycle. Members before uncertainty stay proven.
		if group.kind == "select" && selectionUncertain {
			continue
		}
		members = append(members, member.object)
	}
	if len(members) == 0 && !unknown {
		return []int{group.fallback}, true
	}
	if group.kind == "select" {
		// Selector.Set and its dynamic selection both resolve to the first
		// object with a name. Later same-name objects cannot prove a cycle.
		seen := make(map[string]bool)
		selected := members[:0]
		for _, id := range members {
			if ctx.Err() != nil {
				return nil, false
			}
			if g.objects[id].nameUnknown {
				// The first member is selectable whatever its transformed name.
				// A later unknown name might alias any preceding member. After an
				// unknown name, later known names might also be shadowed.
				if len(seen) == 0 {
					selected = append(selected, id)
				}
				break
			}
			if !seen[g.objects[id].name] {
				selected = append(selected, id)
				seen[g.objects[id].name] = true
			}
		}
		members = selected
	}
	return members, true
}

func policyObjectNameFilter(filter string, object policyProxyObject) policyMatch {
	if !object.nameUnknown {
		return policyNameFilter(filter, object.name)
	}
	if filter == "" {
		return policyMatchYes
	}
	for _, pattern := range strings.Split(filter, "`") {
		if pattern == "" || pattern == ".*" {
			return policyMatchYes
		}
	}
	return policyMatchUnknown
}
