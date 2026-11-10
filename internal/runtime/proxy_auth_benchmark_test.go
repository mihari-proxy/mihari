package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func BenchmarkAuthPreviewLatencyInvestigation(b *testing.B) {
	for _, nodes := range []int{0, 500, 5000, 20000} {
		b.Run(fmt.Sprintf("proxies_%d", nodes), func(b *testing.B) {
			dir := b.TempDir()
			service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(dir, "catalog.yaml"), CacheDir: filepath.Join(dir, "cache")})
			if err != nil {
				b.Fatal(err)
			}
			id := strings.Repeat("a", 32)
			catalog := subscription.Defaults()
			catalog.ActiveID = id
			catalog.Profiles = []subscription.Profile{{ID: id, Name: "Synthetic only", URL: "https://example.invalid/synthetic", Enabled: true, Generation: 1}}
			if err := service.Restore(catalog); err != nil {
				b.Fatal(err)
			}
			var yaml strings.Builder
			if nodes == 0 {
				yaml.WriteString("proxies: []\n")
			} else {
				yaml.WriteString("proxies:\n")
			}
			for i := range nodes {
				fmt.Fprintf(&yaml, "  - {name: synthetic-%d, type: ss, server: example.invalid, port: 443, cipher: aes-128-gcm, password: synthetic-only}\n", i)
			}
			yaml.WriteString("authentication:\n  - fixture:source-synthetic\nskip-auth-prefixes: []\n")
			if err := os.MkdirAll(filepath.Dir(service.CachePath(id)), 0700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(service.CachePath(id), []byte(yaml.String()), 0600); err != nil {
				b.Fatal(err)
			}
			settings := config.Defaults()
			settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "saved-synthetic"}}})
			m := New(Options{Settings: settings, Subscriptions: service, RuntimeConfig: filepath.Join(dir, "runtime.yaml"), StagingDir: filepath.Join(dir, "staging")})
			request := protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "fixture"}}}}
			for _, path := range []string{"read_cache", "preview_warm"} {
				b.Run(path, func(b *testing.B) {
					if path == "preview_warm" {
						if _, err := m.PreviewProxyAuth(context.Background(), request); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportAllocs()
					b.ReportMetric(float64(yaml.Len()), "yaml-bytes")
					b.ResetTimer()
					for range b.N {
						if path == "read_cache" {
							if _, _, err := service.ReadCache(id); err != nil {
								b.Fatal(err)
							}
						} else {
							if _, err := m.PreviewProxyAuth(context.Background(), request); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		})
	}
}
