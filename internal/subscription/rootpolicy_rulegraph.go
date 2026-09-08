package subscription

import "context"

// validateSubruleGraph checks every declared list, including unused lists.
// Real SUB-RULE references are mandatory even when a proxy has the same name;
// the native top-level proxy-name loophole must not hide a missing graph node.
func validateSubruleGraph(ctx context.Context, root policyValue, os string) error {
	sub, _ := root.get("sub-rules")
	names := make(map[string]int, len(sub.fields))
	for i, member := range sub.fields {
		names[member.name] = i
	}
	edges := make([][]int, len(sub.fields))
	type locatedRule struct {
		rule  *policyRule
		field string
	}
	var pending []locatedRule
	checkList := func(list policyValue, index int, field string) error {
		for _, item := range list.items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if item.rule == nil {
				return policyFailure(field)
			}
			pending = append(pending, locatedRule{item.rule, field})
			if item.rule.kind != "SUB-RULE" {
				continue
			}
			target, exists := names[item.rule.target]
			if !exists {
				return policyFailure(field)
			}
			if index >= 0 {
				edges[index] = append(edges[index], target)
			}
		}
		return nil
	}
	rules, _ := root.get("rules")
	if err := checkList(rules, -1, "rules[]"); err != nil {
		return err
	}
	for i, member := range sub.fields {
		if err := checkList(member.value, i, "sub-rules.[entry][]"); err != nil {
			return err
		}
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if item.rule.kind == "UID" && os != "linux" {
			return policyFailure(item.field)
		}
		for _, child := range item.rule.children {
			pending = append(pending, locatedRule{child, item.field})
		}
	}
	return validatePolicyAcyclic(ctx, edges, "sub-rules.[entry][]")
}
