package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_RecursiveRuleSelectedGrammar(t *testing.T) {
	for _, tc := range []struct{ input, output string }{
		{"NOT,((NETWORK,TCP)),DIRECT", "NOT,((NETWORK,TCP)),DIRECT"},
		{"OR,((NETWORK,TCP),(NETWORK,UDP)),DIRECT", "OR,((NETWORK,TCP),(NETWORK,UDP)),DIRECT"},
		{"AND,((NOT,((NETWORK,UDP))),(DST-PORT,443)),DIRECT", "AND,((NOT,((NETWORK,UDP))),(DST-PORT,443)),DIRECT"},
		{"AND,(),DIRECT", "AND,(),DIRECT"}, {"OR,(ignored),DIRECT", "OR,(),DIRECT"},
		{"AND,(NETWORK,TCP),DIRECT", "AND,(),DIRECT"},
		{"AND,(NETWORK,TCP)(NETWORK,UDP),DIRECT", "AND,((NETWORK,TCP),(NETWORK,UDP)),DIRECT"},
		{"AND,((NETWORK,TCP)ignored(NETWORK,UDP)),DIRECT", "AND,((NETWORK,TCP),(NETWORK,UDP)),DIRECT"},
		{"NOT,(ignored(NETWORK,TCP)ignored),DIRECT", "NOT,((NETWORK,TCP)),DIRECT"},
		{"AND,(SUB-RULE-ignored(NETWORK,TCP)),DIRECT", "AND,((NETWORK,TCP)),DIRECT"},
		{"AND,((NETWORK,TCP,DIRECT,unknown)),REJECT", "AND,((NETWORK,TCP)),REJECT"},
		{"AND,((IP-CIDR,192.0.2.1/24,no-resolve),(DOMAIN-REGEX,(?<=example)\\.test{1,2})),DIRECT", "AND,((IP-CIDR,192.0.2.1/24,no-resolve),(DOMAIN-REGEX,(?<=example)\\.test{1,2})),DIRECT"},
	} {
		t.Run(tc.output, func(t *testing.T) {
			input := rootPolicyInput()
			content, err := yaml.Marshal(struct {
				Rules []string `yaml:"rules"`
			}{[]string{tc.input}})
			if err != nil {
				t.Fatal(err)
			}
			input.YAML = content
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("native selected recursive structure rejected: %v", err)
			}
			var got struct {
				Rules []string `yaml:"rules"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Rules) != 1 || got.Rules[0] != tc.output {
				t.Fatal("selected child/order or ignored-text normalization differs")
			}
		})
	}
	for _, line := range []string{
		"NOT,(),DIRECT", "NOT,(NETWORK,TCP),DIRECT", "NOT,((NETWORK,TCP),(NETWORK,UDP)),DIRECT",
		"AND,(()),DIRECT", "NOT,(((NETWORK,TCP))),DIRECT", "AND,((UNKNOWN,x)),DIRECT",
		"AND,((MATCH,DIRECT)),DIRECT", "AND,((SUB-RULE,(NETWORK,TCP),set)),DIRECT",
		"AND,((NETWORK,TCP)),DIRECT,no-resolve", "AND,((NETWORK,TCP),DIRECT", "AND,(())),DIRECT",
		"AND,((DOMAIN-REGEX,\\()),DIRECT", "\tAND,(),DIRECT",
	} {
		input := rootPolicyInput()
		encoded, err := yaml.Marshal(struct {
			Rules []string `yaml:"rules"`
		}{[]string{line}})
		if err != nil {
			t.Fatal(err)
		}
		input.YAML = encoded
		if _, err := NewRootConfigPolicy().Build(context.Background(), input); err == nil {
			t.Fatal("malformed selected logic accepted")
		}
	}
}

func TestRootPolicy_RecursiveRulesHaveNoInventedDepthCap(t *testing.T) {
	line := "NETWORK,TCP"
	for i := 0; i < 256; i++ {
		line = "NOT,((" + line + "))"
	}
	input := rootPolicyInput()
	encoded, err := yaml.Marshal(struct {
		Rules []string `yaml:"rules"`
	}{[]string{line + ",DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	input.YAML = encoded
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("finite deep logic rejected: %v", err)
	}
	if strings.Count(string(out.YAML), "NOT,") != 256 {
		t.Fatal("nested predicates lost")
	}
}

func TestRootPolicy_SubRuleReferences(t *testing.T) {
	for _, body := range []string{
		"sub-rules: {first: ['SUB-RULE,(NETWORK,TCP),later', 'MATCH,REJECT'], later: ['MATCH,DIRECT'], empty: []}\nrules: ['SUB-RULE,(NETWORK,TCP),first']\n",
		"sub-rules: {a: ['SUB-RULE,(NETWORK,TCP),b', 'SUB-RULE,(NETWORK,UDP),c'], b: ['SUB-RULE,(NETWORK,TCP),d'], c: ['SUB-RULE,(NETWORK,TCP),d'], d: []}\n",
		"sub-rules: {'branch / β': ['MATCH,DIRECT'], ' ': [], 'comma,name': []}\nrules: ['SUB-RULE,ignored(NETWORK,TCP)ignored,branch / β']\n",
		"sub-rules: {DIRECT: ['MATCH,REJECT']}\nrules: ['SUB-RULE,(AND,((NETWORK,TCP),(DST-PORT,443))),DIRECT']\n",
		"sub-rules: {empty: null}\nrules: ['SUB-RULE,(NETWORK,TCP),empty']\n",
		"rules: [null]\n", "rules: null\n", "sub-rules: null\n",
	} {
		out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(body))
		if err != nil {
			t.Fatalf("valid sub-rule/empty graph rejected: %v", err)
		}
		if strings.Contains(body, "ignored") && strings.Contains(string(out.YAML), "ignored") {
			t.Fatal("nonselected SUB-RULE text emitted")
		}
	}
	for _, body := range []string{
		"rules: ['SUB-RULE,(NETWORK,TCP),DIRECT']\n",
		"sub-rules: {unused: ['SUB-RULE,(NETWORK,TCP),missing']}\n",
		"sub-rules: {self: ['SUB-RULE,(OR,()),self']}\n",
		"sub-rules: {a: ['SUB-RULE,(NETWORK,TCP),b'], b: ['SUB-RULE,(NETWORK,TCP),a']}\n",
		"sub-rules: {a: ['SUB-RULE,(NETWORK,TCP),A']}\n",
		"sub-rules: {'': []}\n", "sub-rules: {a: 'MATCH,DIRECT'}\n", "sub-rules: {a: [], a: []}\n",
		"sub-rules: {a: []}\nrules: ['SUB-RULE,NETWORK,TCP,a']\n",
		"sub-rules: {a: []}\nrules: ['SUB-RULE,((NETWORK,TCP)),a']\n",
		"sub-rules: {a: []}\nrules: ['SUB-RULE,(NETWORK,TCP),(NETWORK,UDP),a']\n",
		"sub-rules: {a: []}\nrules: ['SUB-RULE,(MATCH,DIRECT),a']\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(body)); err == nil {
			t.Fatal("invalid or cyclic sub-rule graph accepted")
		}
	}
}
