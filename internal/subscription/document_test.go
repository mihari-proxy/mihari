package subscription

import "testing"

func TestParseDocumentAcceptsFullAndNodeOnly(t *testing.T) {
	for name, input := range map[string]string{
		"full": `proxies:
  - {name: one, type: ss, server: 127.0.0.1, port: 443, cipher: aes-128-gcm, password: x}
proxy-groups:
  - {name: select, type: select, proxies: [one]}
rules: [MATCH,select]
`,
		"node-only": `proxies:
  - {name: one, type: ss, server: 127.0.0.1, port: 443, cipher: aes-128-gcm, password: x}
`,
	} {
		t.Run(name, func(t *testing.T) {
			document, err := ParseDocument([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			if len(document) == 0 {
				t.Fatal("empty document")
			}
		})
	}
}

func TestParseDocumentRejectsInvalidNonClashAndMultipleDocuments(t *testing.T) {
	for name, input := range map[string]string{
		"invalid":   "proxies: [",
		"not clash": "hello: world\n",
		"multiple":  "proxies: []\n---\nproxies: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDocument([]byte(input)); err == nil {
				t.Fatal("expected parse failure")
			}
		})
	}
}
