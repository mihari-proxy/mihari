package subscription

import (
	"encoding/base64"
	"math"
)

func tlsMirrorSchema() *policySchema {
	timeSpec := objectSchema(map[string]*policySchema{"base-nanoseconds": unsignedSchema(math.MaxUint64), "uniform-random-multiplier-nanoseconds": unsignedSchema(math.MaxUint64)})
	method := stringSchema()
	method.check = func(v policyValue) bool { return v.text == "" || validHTTPToken(v.text) }
	headerValue := stringSchema()
	headerValue.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	header := objectSchema(map[string]*policySchema{"name": method, "value": headerValue, "values": listSchema(headerValue)})
	transfer := objectSchema(map[string]*policySchema{"weight": integerSchema(math.MinInt32, math.MaxInt32), "goto-location": integerSchema(math.MinInt64, math.MaxInt64)})
	step := objectSchema(map[string]*policySchema{
		"name": stringSchema(), "host": headerValue, "path": stringSchema(), "method": method, "headers": listSchema(header), "next-step": listSchema(transfer),
		"connection-ready": boolSchema(), "connection-recall-exit": boolSchema(), "wait-time": timeSpec, "h2-do-not-wait-for-download-finish": boolSchema(),
	})
	enrolment := objectSchema(map[string]*policySchema{"primary-ingress-outbound": stringSchema(), "primary-egress-outbound": stringSchema()})
	enrolment.nullable = true
	schema := objectSchema(map[string]*policySchema{
		"primary-key": stringSchema(), "explicit-nonce-ciphersuites": listSchema(unsignedSchema(math.MaxUint16)), "defer-instance-derived-write-time": timeSpec,
		"transport-layer-padding": objectSchema(map[string]*policySchema{"enabled": boolSchema()}), "connection-enrolment": enrolment,
		"embedded-traffic-generator": objectSchema(map[string]*policySchema{"steps": listSchema(step)}), "sequence-watermarking-enabled": boolSchema(),
	})
	schema.validate = func(v policyValue, field string) error {
		key, _ := v.get("primary-key")
		if key.text == "" {
			return nil
		}
		decoded, err := base64.StdEncoding.DecodeString(key.text)
		if err != nil || len(decoded) != 32 {
			return policyFailure(field + ".primary-key")
		}
		deferTime, _ := v.get("defer-instance-derived-write-time")
		if !validTLSMirrorTime(deferTime) {
			return policyFailure(field + ".defer-instance-derived-write-time")
		}
		generator, _ := v.get("embedded-traffic-generator")
		steps, _ := generator.get("steps")
		for _, step := range steps.items {
			wait, _ := step.get("wait-time")
			if !validTLSMirrorTime(wait) {
				return policyFailure(field + ".embedded-traffic-generator.steps[].wait-time")
			}
			next, _ := step.get("next-step")
			total := int64(0)
			for _, candidate := range next.items {
				weight, _ := candidate.get("weight")
				total += weight.integer
				if total < math.MinInt32 || total > math.MaxInt32 {
					return policyFailure(field + ".embedded-traffic-generator.steps[].next-step")
				}
			}
			if len(next.items) > 0 && total <= 0 {
				return policyFailure(field + ".embedded-traffic-generator.steps[].next-step")
			}
		}
		return nil
	}
	return schema
}

func validTLSMirrorTime(v policyValue) bool {
	base, _ := v.get("base-nanoseconds")
	spread, _ := v.get("uniform-random-multiplier-nanoseconds")
	if base.unsigned > math.MaxInt64 {
		return false
	}
	return spread.unsigned == 0 || spread.unsigned-1 <= uint64(math.MaxInt64)-base.unsigned
}
