package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
)

type proxyProviderController interface {
	ProxyProviders(context.Context) (mihomo.ProxyProviders, error)
	DelayProviderProxy(context.Context, string, string, string, int) (uint16, error)
}

// ProxyCatalog returns complete display metadata and duplicate names. The raw
// global namespace remains available separately through Proxies for routing.
func (m *Manager) ProxyCatalog(ctx context.Context) (mihomo.Proxies, []string, error) {
	global, err := m.Proxies(ctx)
	if err != nil {
		return mihomo.Proxies{}, nil, fmt.Errorf("read global proxies for catalog: %w", err)
	}
	providers, err := m.proxyProviders(ctx)
	if err != nil {
		return mihomo.Proxies{}, nil, err
	}
	merged, duplicates, _ := resolveProxyCatalog(global, providers)
	return merged, duplicates, nil
}

func resolveProxyCatalog(global mihomo.Proxies, providers mihomo.ProxyProviders) (mihomo.Proxies, []string, map[string]string) {
	merged := mihomo.Proxies{Proxies: make(map[string]mihomo.Proxy, len(global.Proxies))}
	counts := make(map[string]int)
	sources := make(map[string]string)
	for name, node := range global.Proxies {
		merged.Proxies[name] = node
		counts[name]++
	}
	names := make([]string, 0, len(providers.Providers))
	for name := range providers.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, providerName := range names {
		provider := providers.Providers[providerName]
		if provider.VehicleType == "Compatible" {
			continue
		}
		for _, node := range provider.Proxies {
			counts[node.Name]++
			if _, exists := merged.Proxies[node.Name]; !exists {
				merged.Proxies[node.Name] = node
				sources[node.Name] = providerName
			}
		}
	}
	var duplicates []string
	for name, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, name)
		}
	}
	sort.Strings(duplicates)
	return merged, duplicates, sources
}

func (m *Manager) proxyProviders(ctx context.Context) (mihomo.ProxyProviders, error) {
	controller, ok := m.controller.(proxyProviderController)
	if !ok {
		return mihomo.ProxyProviders{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Proxy provider discovery is unavailable"}
	}
	return readProxyProviders(ctx, controller.ProxyProviders, waitProviderRetry, m.diagnosticReporter)
}

func waitProviderRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readProxyProviders(ctx context.Context, read func(context.Context) (mihomo.ProxyProviders, error), wait func(context.Context, time.Duration) error, report diagnostics.Reporter) (mihomo.ProxyProviders, error) {
	// This budget applies only to provider discovery, not the existing global read.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return mihomo.ProxyProviders{}, providerReadError(ctx.Err())
		}
		one, stop := context.WithTimeout(ctx, time.Second)
		result, err := read(one)
		stop()
		if err == nil {
			return result, nil
		}
		last = err
		if ctx.Err() != nil || attempt == 3 || !retryProviderError(err) {
			break
		}
		delay := time.Duration(attempt) * 100 * time.Millisecond
		var retry interface{ RetryAfter() time.Duration }
		if errors.As(err, &retry) && retry.RetryAfter() > delay {
			delay = retry.RetryAfter()
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
			break
		}
		if report != nil {
			report(ctx, diagnostics.Record{Component: "runtime", Event: "proxy_provider.retry", Level: slog.LevelWarn, Err: err})
		}
		if err := wait(ctx, delay); err != nil {
			return mihomo.ProxyProviders{}, providerReadError(err)
		}
	}
	return mihomo.ProxyProviders{}, providerReadError(last)
}

func retryProviderError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		if api.Code == protocol.CodeDataFailure || api.Code == protocol.CodePermissionDenied || api.Code == protocol.CodeInternal {
			return false
		}
		if status, ok := api.Details["status"].(int); ok && (status < 200 || status >= 300) {
			switch status {
			case 408, 429, 500, 502, 503, 504:
				return true
			default:
				return false
			}
		}
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network)
}

func providerReadError(err error) error {
	var api protocol.APIError
	if !errors.As(err, &api) {
		api = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Controller connection failed"}
	}
	api.Message = "Failed to load provider nodes: " + api.Message
	if status, ok := api.Details["status"].(int); ok && (status < 200 || status >= 300) {
		api.Message = fmt.Sprintf("Failed to load provider nodes: mihomo returned HTTP %d", status)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		api.Message = "Failed to load provider nodes: request timed out"
	}
	return diagnostics.Wrap(api, err)
}
