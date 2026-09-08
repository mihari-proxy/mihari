package subscription

import (
	"context"
	"strings"
)

type policyRuleProviderReference struct {
	name, behavior, field string
	owner                 int
}
type policyResourceGraph struct {
	geo            map[GeoResourceKind][]string
	references     []policyRuleProviderReference
	datMode        bool
	linuxOnlyField string
}

func collectPolicyResourceGraph(ctx context.Context, root policyValue, providers []preparedPolicyProvider) (policyResourceGraph, error) {
	graph := policyResourceGraph{geo: make(map[GeoResourceKind][]string)}
	if err := ctx.Err(); err != nil {
		return graph, err
	}
	mode, _ := root.get("geodata-mode")
	graph.datMode = mode.boolean
	rules, _ := root.get("rules")
	if err := graph.rules(ctx, rules, -1, "", "rules[]"); err != nil {
		return graph, err
	}
	subrules, _ := root.get("sub-rules")
	for _, list := range subrules.fields {
		if err := graph.rules(ctx, list.value, -1, "", "sub-rules.[entry][]"); err != nil {
			return graph, err
		}
	}
	for owner, provider := range providers {
		if provider.kind == "rule" && provider.spec.Behavior == "classical" && provider.ready {
			if err := graph.rules(ctx, provider.payload, owner, "", "rule-providers.[entry].resource.payload[]"); err != nil {
				return graph, err
			}
		}
	}
	dns, _ := root.get("dns")
	for _, name := range []string{"nameserver-policy", "proxy-server-nameserver-policy"} {
		policy, _ := dns.get(name)
		for _, entry := range policy.fields {
			for _, selector := range policyDomainSelectors(entry.name, true) {
				graph.selector(selector, "domain", "dns."+name+".[entry]")
			}
		}
	}
	fallback, _ := dns.get("fallback")
	if len(fallback.items) > 0 {
		filter, _ := dns.get("fallback-filter")
		useGeoIP, present := filter.get("geoip")
		if !present || useGeoIP.boolean {
			code, present := filter.get("geoip-code")
			if !present {
				code.text = "CN"
			}
			graph.ip(code.text)
		}
		sites, _ := filter.get("geosite")
		for _, selector := range sites.items {
			graph.geo[GeoSiteDAT] = append(graph.geo[GeoSiteDAT], selector.text)
		}
	}
	if xhttpText(dns, "enhanced-mode") == "fake-ip" {
		filters, _ := dns.get("fake-ip-filter")
		if xhttpText(dns, "fake-ip-filter-mode") == "rule" {
			if err := graph.rules(ctx, filters, -1, "domain", "dns.fake-ip-filter[]"); err != nil {
				return graph, err
			}
		} else {
			for _, entry := range filters.items {
				for _, selector := range policyDomainSelectors(entry.text, false) {
					graph.selector(selector, "domain", "dns.fake-ip-filter[]")
				}
			}
		}
	}
	sniffer, _ := root.get("sniffer")
	for _, name := range []string{"force-domain", "skip-domain"} {
		values, _ := sniffer.get(name)
		for _, value := range values.items {
			for _, selector := range policyDomainSelectors(value.text, false) {
				graph.selector(selector, "domain", "sniffer."+name+"[]")
			}
		}
	}
	for _, name := range []string{"skip-src-address", "skip-dst-address"} {
		values, _ := sniffer.get(name)
		for _, value := range values.items {
			for _, selector := range policyIPSelectors(value.text) {
				graph.selector(selector, "ipcidr", "sniffer."+name+"[]")
			}
		}
	}
	return graph, ctx.Err()
}

func (g *policyResourceGraph) ip(selector string) {
	if strings.ToLower(selector) == "lan" {
		return
	}
	kind := GeoCountryMMDB
	if g.datMode {
		kind = GeoIPDAT
	}
	g.geo[kind] = append(g.geo[kind], selector)
}

func (g *policyResourceGraph) selector(selector policyDomainSelector, behavior, field string) {
	switch selector.kind {
	case "geosite":
		g.geo[GeoSiteDAT] = append(g.geo[GeoSiteDAT], selector.value)
	case "geoip":
		g.ip(selector.value)
	case "rule-set":
		g.references = append(g.references, policyRuleProviderReference{name: selector.value, behavior: behavior, field: field, owner: -1})
	}
}

func (g *policyResourceGraph) rules(ctx context.Context, list policyValue, owner int, behavior, field string) error {
	var pending []*policyRule
	for _, item := range list.items {
		if item.rule == nil {
			return policyFailure(field)
		}
		pending = append(pending, item.rule)
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		rule := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		switch rule.kind {
		case "UID":
			g.linuxOnlyField = field
		case "GEOIP", "SRC-GEOIP":
			g.ip(rule.text)
		case "GEOSITE":
			g.geo[GeoSiteDAT] = append(g.geo[GeoSiteDAT], rule.text)
		case "IP-ASN", "SRC-IP-ASN":
			g.geo[GeoASNMMDB] = nil
		case "RULE-SET":
			g.references = append(g.references, policyRuleProviderReference{name: rule.text, behavior: behavior, field: field, owner: owner})
		}
		pending = append(pending, rule.children...)
	}
	return nil
}

func validateRuleProviderReferences(ctx context.Context, graph policyResourceGraph, providers []preparedPolicyProvider) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	names := make(map[string]int)
	for i, provider := range providers {
		if provider.kind == "rule" {
			names[provider.name] = i
		}
	}
	edges := make([][]int, len(providers))
	for _, reference := range graph.references {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, exists := names[reference.name]
		if !exists {
			return policyFailure(reference.field)
		}
		behavior := providers[target].spec.Behavior
		if reference.behavior == "domain" && behavior == "ipcidr" || reference.behavior == "ipcidr" && behavior == "domain" {
			return policyFailure(reference.field)
		}
		if reference.owner >= 0 {
			edges[reference.owner] = append(edges[reference.owner], target)
		}
	}
	return validatePolicyAcyclic(ctx, edges, "rule-providers.[entry].resource.payload[]")
}

func validatePolicyAcyclic(ctx context.Context, edges [][]int, field string) error {
	type frame struct{ node, next int }
	colors := make([]uint8, len(edges))
	for start := range edges {
		if colors[start] != 0 {
			continue
		}
		colors[start] = 1
		stack := []frame{{node: start}}
		for len(stack) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			current := &stack[len(stack)-1]
			if current.next == len(edges[current.node]) {
				colors[current.node] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			target := edges[current.node][current.next]
			current.next++
			if colors[target] == 1 {
				return policyFailure(field)
			}
			if colors[target] == 0 {
				colors[target] = 1
				stack = append(stack, frame{node: target})
			}
		}
	}
	return ctx.Err()
}
