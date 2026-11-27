package protocol

import "slices"

// Clone returns an independent copy of the fragment list and each mutable DNS field.
func (input ConfigOverrides) Clone() ConfigOverrides {
	result := ConfigOverrides{Fragments: slices.Clone(input.Fragments)}
	if len(input.DNS) > 0 {
		result.DNS = make(map[string]DNSFieldOverride, len(input.DNS))
		for k, v := range input.DNS {
			if v.Bool != nil {
				b := *v.Bool
				v.Bool = &b
			}
			v.Servers = slices.Clone(v.Servers)
			result.DNS[k] = v
		}
	}
	return result
}
