package subscription

import (
	"context"
	"sort"
	"strings"
)

// These objects exist before root proxies in the pinned config parser. GLOBAL
// is created later and deliberately is not reserved here.
var policyBuiltinProxyNames = [...]string{"DIRECT", "REJECT", "REJECT-DROP", "COMPATIBLE", "PASS", "PASS-RULE"}

type policyProxyObject struct {
	name, kind     string
	nameUnknown    bool
	value          policyValue
	group          bool
	members        []policyProxyMember
	unknownMembers bool
	fallback       int
}

type policyProxyMember struct {
	object int
	// local Compatible-provider members bypass the group's positive filter.
	local bool
}

type policyProxyGraph struct {
	objects         []policyProxyObject
	names           map[string]int
	providerObjects map[string][]int
	providerMembers map[string][]int
	providerReady   map[string]bool
	providerUnknown map[string]bool
}

func validatePolicyProxyGraph(ctx context.Context, root policyValue, providers []preparedPolicyProvider) error {
	g, err := assemblePolicyProxyGraph(ctx, root, providers)
	if err != nil {
		return err
	}
	edges := make([][]int, len(g.objects))
	for id, object := range g.objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if object.group {
			members, known := g.knownGroupMembers(ctx, object)
			if err := ctx.Err(); err != nil {
				return err
			}
			if known {
				edges[id] = append(edges[id], members...)
			}
			continue
		}
		switch object.kind {
		case "direct", "dns", "reject", "rematch":
			continue
		}
		dialer := xhttpText(object.value, "dialer-proxy")
		if dialer == "" {
			continue
		}
		target, exists := g.names[dialer]
		if !exists {
			return policyFailure("proxies[].dialer-proxy")
		}
		edges[id] = append(edges[id], target)
	}
	if err := validatePolicyAcyclic(ctx, edges, "proxies[].dialer-proxy"); err != nil {
		return err
	}
	lists := []policyValue{}
	rules, _ := root.get("rules")
	lists = append(lists, rules)
	subrules, _ := root.get("sub-rules")
	for _, member := range subrules.fields {
		lists = append(lists, member.value)
	}
	for _, list := range lists {
		for _, item := range list.items {
			if item.rule != nil && item.rule.hasTarget && item.rule.kind != "SUB-RULE" {
				if _, exists := g.names[item.rule.target]; !exists {
					return policyFailure("rules[].target")
				}
			}
		}
	}
	if err := validatePolicyNTPProxyReference(root, g); err != nil {
		return err
	}
	return validatePolicyDNSProxyReferences(ctx, root, g)
}

// NTP uses NewByName only for an enabled, running service. Unlike DNS URI
// fragments this field has neither an interface fallback nor a RULES sentinel.
func validatePolicyNTPProxyReference(root policyValue, graph policyProxyGraph) error {
	ntp, _ := root.get("ntp")
	enable, _ := ntp.get("enable")
	if !enable.boolean {
		return nil
	}
	if interval, supplied := ntp.get("interval"); supplied && interval.integer <= 0 {
		return nil
	}
	name := xhttpText(ntp, "dialer-proxy")
	if name != "" {
		if _, exists := graph.names[name]; !exists {
			return policyFailure("ntp.dialer-proxy")
		}
	}
	return nil
}

func assemblePolicyProxyGraph(ctx context.Context, root policyValue, providers []preparedPolicyProvider) (policyProxyGraph, error) {
	g := policyProxyGraph{names: make(map[string]int), providerObjects: make(map[string][]int), providerMembers: make(map[string][]int), providerReady: make(map[string]bool), providerUnknown: make(map[string]bool)}
	fail := func(field string) (policyProxyGraph, error) { return policyProxyGraph{}, policyFailure(field) }
	for _, name := range policyBuiltinProxyNames {
		g.names[name] = len(g.objects)
		g.objects = append(g.objects, policyProxyObject{name: name, kind: name})
	}
	// The default provider captures these two built-ins and each explicit root
	// and group object in source order, before the automatic GLOBAL overwrite.
	captured := []int{g.names["DIRECT"], g.names["REJECT"]}
	var allProxies []string
	proxies, _ := root.get("proxies")
	for _, proxy := range proxies.items {
		if err := ctx.Err(); err != nil {
			return policyProxyGraph{}, err
		}
		name := xhttpText(proxy, "name")
		if _, exists := g.names[name]; exists {
			return fail("proxies[].name")
		}
		g.names[name] = len(g.objects)
		captured = append(captured, len(g.objects))
		g.objects = append(g.objects, policyProxyObject{name: name, kind: xhttpText(proxy, "type"), value: proxy})
		allProxies = append(allProxies, name)
	}
	sort.Strings(allProxies)
	groups, _ := root.get("proxy-groups")
	groupNames := make(map[string]int)
	groupObjects := make([]int, len(groups.items))
	for i, group := range groups.items {
		name := xhttpText(group, "name")
		if _, exists := g.names[name]; exists {
			return fail("proxy-groups[].name")
		}
		if _, exists := groupNames[name]; exists {
			return fail("proxy-groups[].name")
		}
		groupNames[name] = i
		groupObjects[i] = len(g.objects)
		captured = append(captured, len(g.objects))
		g.objects = append(g.objects, policyProxyObject{name: name, kind: xhttpText(group, "type"), value: group, group: true})
	}
	order, err := policyGroupOrder(ctx, groups, groupNames)
	if err != nil {
		return policyProxyGraph{}, err
	}
	var allProviders []string
	for _, provider := range providers {
		if provider.kind != "proxy" {
			continue
		}
		if provider.name == "default" {
			return fail("proxy-providers.[entry]")
		}
		allProviders = append(allProviders, provider.name)
		g.providerReady[provider.name] = provider.ready
		for _, proxy := range provider.payload.items {
			g.providerObjects[provider.name] = append(g.providerObjects[provider.name], len(g.objects))
			g.objects = append(g.objects, policyProxyObject{name: xhttpText(proxy, "name"), kind: xhttpText(proxy, "type"), value: proxy})
		}
		members, known := g.knownProviderMembers(ctx, provider, g.providerObjects[provider.name])
		if err := ctx.Err(); err != nil {
			return policyProxyGraph{}, err
		}
		if known && len(members) == 0 {
			return fail("proxy-providers.[entry].filter")
		}
		g.providerMembers[provider.name] = members
		g.providerUnknown[provider.name] = !known
	}
	sort.Strings(allProviders)
	synthetic := make(map[string]bool)
	for _, index := range order {
		if err := ctx.Err(); err != nil {
			return policyProxyGraph{}, err
		}
		group := groups.items[index]
		id := groupObjects[index]
		name := xhttpText(group, "name")
		fallbackName := xhttpText(group, "empty-fallback")
		if fallbackName == "" {
			fallbackName = "COMPATIBLE"
		}
		fallback, exists := g.names[fallbackName]
		if !exists || g.objects[fallback].group {
			return fail("proxy-groups[].empty-fallback")
		}
		g.objects[id].fallback = fallback
		use := policyProxyStringList(group, "use")
		if policyProxyBool(group, "include-all") || policyProxyBool(group, "include-all-providers") {
			use = allProviders
		}
		local := policyProxyStringList(group, "proxies")
		unknownLocal := false
		include := policyProxyBool(group, "include-all") || policyProxyBool(group, "include-all-proxies")
		if include {
			for _, proxy := range allProxies {
				switch policyNameFilter(xhttpText(group, "filter"), proxy) {
				case policyMatchYes:
					local = append(local, proxy)
				case policyMatchUnknown:
					unknownLocal = true
				}
			}
			if len(local) == 0 && len(use) == 0 && !unknownLocal {
				local = []string{fallbackName}
			}
		}
		if len(local) == 0 && len(use) == 0 && !unknownLocal {
			return fail("proxy-groups[].proxies")
		}
		for _, provider := range use {
			ready, declared := g.providerReady[provider]
			if !declared || synthetic[provider] {
				return fail("proxy-groups[].use[]")
			}
			if !ready || g.providerUnknown[provider] {
				g.objects[id].unknownMembers = true
			}
		}
		// Compatible providers are prepended to explicit Use providers.
		for _, proxy := range local {
			target, exists := g.names[proxy]
			if !exists {
				return fail("proxy-groups[].proxies[]")
			}
			g.objects[id].members = append(g.objects[id].members, policyProxyMember{object: target, local: true})
		}
		if len(local) != 0 || unknownLocal && len(use) == 0 {
			if _, exists := g.providerReady[name]; exists {
				return fail("proxy-groups[].name")
			}
			if synthetic[name] {
				return fail("proxy-groups[].name")
			}
			synthetic[name] = true
		}
		g.objects[id].unknownMembers = g.objects[id].unknownMembers || unknownLocal
		for _, provider := range use {
			for _, target := range g.providerMembers[provider] {
				g.objects[id].members = append(g.objects[id].members, policyProxyMember{object: target})
			}
		}
		g.names[name] = id
	}
	if _, explicit := groupNames["GLOBAL"]; !explicit {
		global := policyProxyObject{name: "GLOBAL", kind: "select", group: true}
		for _, id := range captured {
			global.members = append(global.members, policyProxyMember{object: id, local: true})
		}
		g.names["GLOBAL"] = len(g.objects)
		g.objects = append(g.objects, global)
	}
	return g, nil
}

func policyProxyStringList(v policyValue, name string) []string {
	list, _ := v.get(name)
	values := make([]string, 0, len(list.items))
	for _, item := range list.items {
		values = append(values, item.text)
	}
	return values
}

func policyProxyBool(v policyValue, name string) bool { value, _ := v.get(name); return value.boolean }

func policyGroupOrder(ctx context.Context, groups policyValue, names map[string]int) ([]int, error) {
	edges := make([][]int, len(groups.items))
	reverse := make([][]int, len(groups.items))
	pending := make([]int, len(groups.items))
	for i, group := range groups.items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, name := range policyProxyStringList(group, "proxies") {
			if target, exists := names[name]; exists {
				edges[i] = append(edges[i], target)
				reverse[target] = append(reverse[target], i)
				pending[i]++
			}
		}
	}
	if err := validatePolicyAcyclic(ctx, edges, "proxy-groups[].proxies[]"); err != nil {
		return nil, err
	}
	var order []int
	for i, count := range pending {
		if count == 0 {
			order = append(order, i)
		}
	}
	for next := 0; next < len(order); next++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, dependent := range reverse[order[next]] {
			pending[dependent]--
			if pending[dependent] == 0 {
				order = append(order, dependent)
			}
		}
	}
	return order, nil
}

type policyMatch uint8

const (
	policyMatchUnknown policyMatch = iota
	policyMatchNo
	policyMatchYes
)

// policyNameFilter proves only a small, sound subset of regexp2 matches. All
// other expressions remain accepted and unknown; this is not a RE2 substitute.
func policyNameFilter(filter, name string) policyMatch {
	if filter == "" {
		return policyMatchYes
	}
	unknown := false
	for _, part := range strings.Split(filter, "`") {
		match := policyNamePattern(part, name)
		if match == policyMatchYes {
			return match
		}
		unknown = unknown || match == policyMatchUnknown
	}
	if unknown {
		return policyMatchUnknown
	}
	return policyMatchNo
}

func policyNamePattern(pattern, name string) policyMatch {
	if pattern == "" || pattern == ".*" {
		return policyMatchYes
	}
	// Plain ASCII text has no encoding, Unicode-category or regex options.
	for _, char := range pattern {
		if char > 127 || strings.ContainsRune(`\.^$*+?()[]{}|`, char) {
			return policyMatchUnknown
		}
	}
	if strings.Contains(name, pattern) {
		return policyMatchYes
	}
	return policyMatchNo
}
