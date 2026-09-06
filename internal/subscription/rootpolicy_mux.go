package subscription

import (
	"math"
	"regexp"
	"strconv"
)

func muxSchema() *policySchema {
	fields := map[string]*policySchema{
		"enabled": boolSchema(), "protocol": stringSchema(), "padding": boolSchema(), "statistic": boolSchema(), "only-tcp": boolSchema(),
		"max-connections": integerSchema(math.MinInt64, math.MaxInt64),
		"min-streams":     integerSchema(math.MinInt64, math.MaxInt64), "max-streams": integerSchema(math.MinInt64, math.MaxInt64),
		"brutal-opts": objectSchema(map[string]*policySchema{"enabled": boolSchema(), "up": rateSchema(), "down": rateSchema()}),
	}
	schema := objectSchema(fields)
	schema.validate = func(v policyValue, field string) error {
		enabled, _ := v.get("enabled")
		protocol, _ := v.get("protocol")
		if enabled.boolean {
			switch protocol.text {
			case "", "h2mux", "smux", "yamux":
			default:
				return policyFailure(field + ".protocol")
			}
		}
		return nil
	}
	return schema
}

// Match the pinned network rate grammar, preserving invalid-text=>zero and
// signed bare-number behavior. Only actual uint64 conversion/product overflow
// is rejected; no arbitrary bandwidth ceiling is imposed.
var policyRatePattern = regexp.MustCompile(`^(\d+)\s*([KMGT]?)([Bb])ps$`)

func policyRate(value string) (uint64, bool) {
	if value == "" {
		return 0, true
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n < 0 {
			return 0, true
		}
		value = strconv.FormatInt(n, 10) + " Mbps"
	}
	parts := policyRatePattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, true
	}
	number, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	factor := uint64(1)
	switch parts[2] {
	case "T":
		factor = 1000000000000
	case "G":
		factor = 1000000000
	case "M":
		factor = 1000000
	case "K":
		factor = 1000
	}
	if number > math.MaxUint64/factor {
		return 0, false
	}
	result := number * factor
	if parts[3] == "b" {
		result /= 8
	}
	return result, true
}

func rateSchema() *policySchema {
	schema := stringSchema()
	schema.check = func(v policyValue) bool { _, ok := policyRate(v.text); return ok }
	return schema
}
