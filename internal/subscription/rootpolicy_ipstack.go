package subscription

import (
	"encoding/binary"
	"net/netip"
)

// IP stacks here are userspace packet processors. The pinned adapters create
// internal NICs/routes; they never select an OS device or a filesystem path.
func ipStackSchema() *policySchema {
	return objectSchema(map[string]*policySchema{
		"mode":                  enumSchema("", "auto", "gvisor", "mips"),
		"congestion-controller": enumSchema("", "cubic", "reno", "bbr", "bbr3"),
	})
}

func validPolicyIPStack(v policyValue, prefixes []netip.Prefix, mtu uint32) bool {
	mode, _ := v.get("mode")
	if mode.text != "mips" {
		// The selected official artifact has with_gvisor. Its stack accepts a
		// uint32 MTU; mipstack's narrower numeric rules do not apply to it.
		return true
	}
	if mtu < 68 || mtu > 65535 {
		return false
	}
	addresses := make([]netip.Addr, 0, len(prefixes))
	broadcasts := make(map[netip.Addr]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		address := prefix.Addr().Unmap()
		if !prefix.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
			return false
		}
		bits := prefix.Bits()
		if prefix.Addr().Is6() && address.Is4() {
			bits -= 96
		}
		if bits < 0 || bits > address.BitLen() {
			return false
		}
		if address.Is6() && mtu < 1280 {
			return false
		}
		if address.Is4() {
			bytes := address.As4()
			value := binary.BigEndian.Uint32(bytes[:])
			if value == ^uint32(0) {
				return false
			}
			if bits < 31 {
				mask := ^uint32(0) >> bits
				broadcast := value | mask
				if value == broadcast {
					return false
				}
				if !address.IsLoopback() {
					var raw [4]byte
					binary.BigEndian.PutUint32(raw[:], broadcast)
					broadcasts[netip.AddrFrom4(raw)] = struct{}{}
				}
			}
		}
		addresses = append(addresses, address)
	}
	for _, address := range addresses {
		if _, exists := broadcasts[address]; exists {
			return false
		}
	}
	return len(addresses) != 0
}
