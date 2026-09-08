package subscription

import "context"

func validatePolicyDNSProxyReferences(ctx context.Context, root policyValue, graph policyProxyGraph) error {
	dns, _ := root.get("dns")
	check := func(list policyValue, field string) error {
		for _, item := range list.items {
			if err := ctx.Err(); err != nil {
				return err
			}
			server, err := parsePolicyNameserver(item.text)
			if err != nil {
				return policyFailure(field)
			}
			if server.scheme == "ts" || server.scheme == "tailscale" {
				// This URI is a reference, not a filename. Its consumer needs a
				// Tailscale outbound registered resolver; the root capability
				// registry currently rejects that stateful outbound family.
				id, exists := graph.names[server.host]
				if !exists || graph.objects[id].kind != "tailscale" {
					return policyFailure(field + ".outbound")
				}
			}
			// Other bare selectors are RULES, an existing proxy, or a socket
			// interface name. Missing proxy lookup is valid interface routing.
		}
		return nil
	}
	for _, field := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "direct-nameserver"} {
		list, _ := dns.get(field)
		if err := check(list, "dns."+field+"[]"); err != nil {
			return err
		}
	}
	for _, field := range []string{"nameserver-policy", "proxy-server-nameserver-policy"} {
		policy, _ := dns.get(field)
		for _, member := range policy.fields {
			if err := check(member.value, "dns."+field+".[entry][]"); err != nil {
				return err
			}
		}
	}
	for _, object := range graph.objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if object.kind != "wireguard" && object.kind != "masque" && object.kind != "openvpn" {
			continue
		}
		remote, _ := object.value.get("remote-dns-resolve")
		if !remote.boolean {
			continue
		}
		servers, _ := object.value.get("dns")
		// These active outbound consumers supply themselves as ProxyAdapter, so bare
		// selectors cannot create a new proxy edge. The Tailscale DNS client
		// ignores that adapter and still requires its named registered resolver.
		if err := check(servers, "proxies[].dns[]"); err != nil {
			return err
		}
	}
	return nil
}
