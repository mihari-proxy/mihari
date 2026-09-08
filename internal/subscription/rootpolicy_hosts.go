package subscription

import (
	"context"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

func stringListUnion(element *policySchema) *policySchema {
	schema := listSchema(element)
	schema.scalarStringList = true
	return schema
}

func hostsSchema() *policySchema {
	values := stringListUnion(stringSchema())
	values.check = func(v policyValue) bool {
		if len(v.items) == 0 {
			return false
		}
		for _, item := range v.items {
			if _, err := netip.ParseAddr(item.text); err == nil {
				continue
			}
			if len(v.items) != 1 {
				return false
			}
			if item.text != "lan" && !strings.Contains(strings.Trim(item.text, "."), ".") {
				return false
			}
		}
		return true
	}
	schema := &policySchema{kind: policyObject, dynamic: values, normalize: strings.ToLower}
	schema.transform = validatePolicyHosts
	return schema
}

// policyDomainParts mirrors the fixed trie grammar, not a DNS hostname or
// filesystem-path grammar. Interior label text other than misplaced wildcards
// is data; imposing hostname character restrictions would change matching.
func policyDomainParts(domain string) ([]string, bool) {
	if domain == "" || strings.HasSuffix(domain, ".") {
		return nil, false
	}
	first, _ := utf8.DecodeRuneInString(domain)
	last, _ := utf8.DecodeLastRuneInString(domain)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return nil, false
	}
	parts := strings.Split(strings.ToLower(domain), ".")
	for i, part := range parts {
		if part == "" && (i != 0 || len(parts) == 1) {
			return nil, false
		}
		if strings.Contains(part, "+") && (part != "+" || i != 0 || len(parts) == 1) {
			return nil, false
		}
		if strings.Contains(part, "*") && part != "*" {
			return nil, false
		}
	}
	return parts, true
}

// policyDomainTrie tracks exact normalized insertion identities and implements
// the native literal, star, dot-wildcard search priority. Iterative traversal
// avoids using attacker-controlled label/alias depth as Go call-stack depth.
type policyDomainTrie struct {
	children map[string]*policyDomainTrie
	index    int
}

func newPolicyDomainTrie() *policyDomainTrie { return &policyDomainTrie{index: -1} }

func (t *policyDomainTrie) insert(parts []string, index int) bool {
	for i := len(parts) - 1; i >= 0; i-- {
		if t.children == nil {
			t.children = make(map[string]*policyDomainTrie)
		}
		next := t.children[parts[i]]
		if next == nil {
			next = newPolicyDomainTrie()
			t.children[parts[i]] = next
		}
		t = next
	}
	if t.index >= 0 {
		return false
	}
	t.index = index
	return true
}

func (t *policyDomainTrie) add(domain string, index int) bool {
	parts, ok := policyDomainParts(domain)
	if !ok {
		return false
	}
	if parts[0] == "+" {
		if !t.insert(parts[1:], index) {
			return false
		}
		parts[0] = ""
	}
	return t.insert(parts, index)
}

func (t *policyDomainTrie) search(ctx context.Context, domain string) (int, error) {
	parts, ok := policyDomainParts(domain)
	if !ok || parts[0] == "" {
		return -1, nil
	}
	type pending struct {
		node      *policyDomainTrie
		remaining int
	}
	stack := []pending{{t, len(parts)}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.remaining == 0 {
			if current.node.index >= 0 {
				return current.node.index, nil
			}
			continue
		}
		if dot := current.node.children[""]; dot != nil {
			stack = append(stack, pending{dot, 0})
		}
		label := parts[current.remaining-1]
		if star := current.node.children["*"]; star != nil && label != "*" {
			stack = append(stack, pending{star, current.remaining - 1})
		}
		if literal := current.node.children[label]; literal != nil {
			stack = append(stack, pending{literal, current.remaining - 1})
		}
	}
	return -1, nil
}

func validatePolicyHosts(ctx context.Context, hosts policyValue, field string) (policyValue, error) {
	trie := newPolicyDomainTrie()
	for i, member := range hosts.fields {
		if err := ctx.Err(); err != nil {
			return policyValue{}, err
		}
		if !trie.add(member.name, i) {
			return policyValue{}, policyFailure(field + ".[entry]")
		}
	}
	edges := make([]int, len(hosts.fields))
	for i, member := range hosts.fields {
		edges[i] = -1
		items := member.value.items
		if len(items) != 1 || items[0].text == "lan" {
			continue
		}
		if _, err := netip.ParseAddr(items[0].text); err == nil {
			continue
		}
		target := strings.Trim(items[0].text, ".")
		index, err := trie.search(ctx, target)
		if err != nil {
			return policyValue{}, err
		}
		edges[i] = index
	}
	// Every alias has at most one effective successor after trie priority. A
	// three-color walk proves the complete graph without quadratic chain walks.
	colors := make([]uint8, len(edges))
	for start := range edges {
		if colors[start] != 0 {
			continue
		}
		index := start
		for index >= 0 && colors[index] == 0 {
			if err := ctx.Err(); err != nil {
				return policyValue{}, err
			}
			colors[index] = 1
			index = edges[index]
		}
		if index >= 0 && colors[index] == 1 {
			return policyValue{}, policyFailure(field + ".[entry]")
		}
		for index = start; index >= 0 && colors[index] == 1; index = edges[index] {
			colors[index] = 2
		}
	}
	return hosts, nil
}
