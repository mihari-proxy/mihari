package platform

import (
	"context"
	"fmt"
	"net"
	"sort"
)

// NetworkInterface contains local adapter identity and link state, not reachability.
type NetworkInterface struct {
	Name         string
	Kind         string
	Availability string
	Addresses    []string
}

// NetworkInterfaces enumerates all adapters, including disabled and disconnected ones.
func NetworkInterfaces(ctx context.Context) ([]NetworkInterface, error) {
	return enumerateNetworkInterfaces(ctx, net.Interfaces, func(adapter net.Interface) ([]net.Addr, error) { return adapter.Addrs() })
}

func enumerateNetworkInterfaces(ctx context.Context, list func() ([]net.Interface, error), addrs func(net.Interface) ([]net.Addr, error)) ([]NetworkInterface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interfaces, err := list()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	result := make([]NetworkInterface, 0, len(interfaces))
	for _, adapter := range interfaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := NetworkInterface{Name: adapter.Name, Kind: "unknown", Availability: "disconnected", Addresses: []string{}}
		if adapter.Flags&net.FlagUp != 0 && adapter.Flags&net.FlagRunning != 0 {
			item.Availability = "available"
		}
		if adapter.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			item.Kind = "virtual"
		}
		addresses, err := addrs(adapter)
		if err != nil {
			return nil, fmt.Errorf("read interface %q addresses: %w", adapter.Name, err)
		}
		for _, address := range addresses {
			item.Addresses = append(item.Addresses, address.String())
		}
		sort.Strings(item.Addresses)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
