package subscription

import (
	"context"
	"strings"
)

type policyGeoBudget struct {
	count int
	bytes int64
}

func (b *policyGeoBudget) add(size int64) error {
	if b.count >= 4 || size < 0 || size > maxGeoResourceBytes || size > 4*maxGeoResourceBytes-b.bytes {
		return policyFailure("geo.budget")
	}
	b.count++
	b.bytes += size
	return nil
}

func preparePolicyGeo(ctx context.Context, input PolicyInput, root policyValue, graph policyResourceGraph, complete bool) ([]GeoResourceKind, []GeoResourceSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	kinds := [...]GeoResourceKind{GeoCountryMMDB, GeoASNMMDB, GeoIPDAT, GeoSiteDAT}
	registered := make(map[string]GeoResourceKind, len(kinds))
	ids := make(map[GeoResourceKind]string, len(kinds))
	for _, kind := range kinds {
		id, err := GeoResourceID(kind)
		if err != nil {
			return nil, nil, err
		}
		registered[id], ids[kind] = kind, id
	}
	var budget policyGeoBudget
	for id, data := range input.Resources {
		if !strings.HasPrefix(id, "mihari.geo/") {
			continue
		}
		if _, known := registered[id]; !known {
			return nil, nil, policyFailure("geo.resource-id")
		}
		if err := budget.add(int64(len(data))); err != nil {
			return nil, nil, err
		}
	}
	for kind := range graph.geo {
		if _, err := GeoResourcePath(kind); err != nil {
			return nil, nil, err
		}
	}
	_, geoIPActive := graph.geo[GeoIPDAT]
	_, geoSiteActive := graph.geo[GeoSiteDAT]
	if geoIPActive || geoSiteActive {
		loader, declared := root.get("geodata-loader")
		if !declared {
			loader.text = "memconservative"
		}
		switch loader.text {
		case "memconservative", "memc", "standard":
		default:
			return nil, nil, policyFailure("geodata-loader")
		}
	}
	var wanted []GeoResourceKind
	var output []GeoResourceSpec
	for _, kind := range kinds {
		selectors, required := graph.geo[kind]
		if required {
			wanted = append(wanted, kind)
		}
		data, provided := input.Resources[ids[kind]]
		if !provided {
			if required && complete {
				return nil, nil, policyFailure("geo.resource")
			}
			continue
		}
		var resource GeoResourceSpec
		var err error
		if kind == GeoIPDAT || kind == GeoSiteDAT {
			resource, err = validateGeoDAT(ctx, kind, data, selectors, xhttpText(root, "geosite-matcher"))
		} else {
			resource, err = validateGeoMMDB(ctx, kind, data)
		}
		if err != nil {
			return nil, nil, err
		}
		output = append(output, resource)
	}
	return wanted, output, nil
}
