package subscription

import (
	"context"
	"sort"
	"strings"
)

func isPolicyLogic(kind string) bool {
	return kind == "NOT" || kind == "OR" || kind == "AND" || kind == "SUB-RULE"
}

// policyParen indexes matched delimiters in opening order. A single scan
// records sibling/child links, including balanced parentheses inside regexes:
// the native scanner has no regex quoting or escaping state either.
type policyParen struct{ open, close, first, last, next int }

func parsePolicyLogic(ctx context.Context, root *policyRule, field string) error {
	text := root.text
	var pairs []policyParen
	var nesting []int
	lastRoot := -1
	for i := range len(text) {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		switch text[i] {
		case '(':
			index := len(pairs)
			pairs = append(pairs, policyParen{open: i, close: -1, first: -1, last: -1, next: -1})
			if len(nesting) > 0 {
				parent := nesting[len(nesting)-1]
				if pairs[parent].first < 0 {
					pairs[parent].first = index
				} else {
					pairs[pairs[parent].last].next = index
				}
				pairs[parent].last = index
			} else {
				if lastRoot >= 0 {
					pairs[lastRoot].next = index
				}
				lastRoot = index
			}
			nesting = append(nesting, index)
		case ')':
			if len(nesting) == 0 {
				return policyFailure(field)
			}
			index := nesting[len(nesting)-1]
			nesting = nesting[:len(nesting)-1]
			pairs[index].close = i
		}
	}
	if len(nesting) != 0 {
		return policyFailure(field)
	}
	type work struct {
		rule       *policyRule
		start, end int
	}
	pending := []work{{root, 0, len(text)}}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.start >= current.end {
			return policyFailure(field)
		}
		first := sort.Search(len(pairs), func(i int) bool { return pairs[i].open >= current.start })
		if current.rule.kind != "SUB-RULE" {
			if text[current.start] != '(' || text[current.end-1] != ')' || first == len(pairs) || pairs[first].open != current.start {
				return policyFailure(field)
			}
			if pairs[first].close == current.end-1 {
				first = pairs[first].first
			}
		}
		for index := first; index >= 0 && index < len(pairs) && pairs[index].open < current.end; index = pairs[index].next {
			pair := pairs[index]
			if pair.close >= current.end {
				return policyFailure(field)
			}
			body := text[pair.open+1 : pair.close]
			child, params := policyRuleHeader(body, false)
			if child.kind == "MATCH" || child.kind == "SUB-RULE" || child.kind == "" || child.text == "" {
				return policyFailure(field)
			}
			current.rule.children = append(current.rule.children, child)
			if isPolicyLogic(child.kind) {
				start := pair.open + 1 + strings.IndexByte(body, ',') + 1
				end := pair.close
				for start < end && text[start] == ' ' {
					start++
				}
				for end > start && text[end-1] == ' ' {
					end--
				}
				pending = append(pending, work{child, start, end})
			} else if err := validatePolicyRuleLeaf(child, params, field); err != nil {
				return err
			}
		}
		if (current.rule.kind == "NOT" || current.rule.kind == "SUB-RULE") && len(current.rule.children) != 1 {
			return policyFailure(field)
		}
		current.rule.text = ""
	}
	return nil
}

// encode serializes the validated selected AST rather than the native display
// Payload(), which is not configuration grammar. Iterative emission keeps both
// parser and output stack use independent of caller-chosen logic depth.
func (r *policyRule) encode() string {
	type event struct {
		rule *policyRule
		text string
	}
	stack := []event{{rule: r}}
	var out strings.Builder
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if item.rule == nil {
			out.WriteString(item.text)
			continue
		}
		node := item.rule
		if !isPolicyLogic(node.kind) {
			out.WriteString(node.encodeLeaf())
			continue
		}
		out.WriteString(node.kind)
		out.WriteString(",(")
		end := ")"
		if node.hasTarget {
			end += "," + node.target
		}
		stack = append(stack, event{text: end})
		if node.kind == "SUB-RULE" {
			stack = append(stack, event{rule: node.children[0]})
		} else {
			for i := len(node.children) - 1; i >= 0; i-- {
				stack = append(stack, event{text: ")"}, event{rule: node.children[i]}, event{text: "("})
				if i > 0 {
					stack = append(stack, event{text: ","})
				}
			}
		}
	}
	return out.String()
}
