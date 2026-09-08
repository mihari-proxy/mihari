package subscription

import "strings"

func sudokuProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	key := stringSchema()
	key.check = func(v policyValue) bool { return v.text != "" }
	fields["key"] = key
	fields["aead-method"] = &policySchema{kind: policyString, choices: []string{"", "aes-128-gcm", "chacha20-poly1305", "none"}}
	padding := integerSchema(0, 100)
	padding.nullable = true
	fields["padding-min"], fields["padding-max"] = padding, padding
	pointerBool := boolSchema()
	pointerBool.nullable = true
	fields["enable-pure-downlink"], fields["http-mask"] = pointerBool, pointerBool
	fields["http-mask-tls"] = boolSchema()
	for _, name := range []string{"table-type", "http-mask-mode", "http-mask-host", "path-root", "multiplex", "http-mask-multiplex", "custom-table"} {
		fields[name] = stringSchema()
	}
	fields["custom-tables"] = listSchema(stringSchema())
	httpmask := objectSchema(map[string]*policySchema{
		"disable": boolSchema(), "mode": stringSchema(), "tls": boolSchema(), "host": stringSchema(), "path-root": stringSchema(), "multiplex": stringSchema(),
	})
	httpmask.nullable = true
	fields["httpmask"] = httpmask
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "key")
	schema.validate = validateSudoku
	return schema
}

func validateSudoku(v policyValue, field string) error {
	minimum, hasMin := v.get("padding-min")
	maximum, hasMax := v.get("padding-max")
	if hasMin && hasMax && minimum.kind != policyNull && maximum.kind != policyNull && minimum.integer > maximum.integer {
		return policyFailure(field + ".padding-max")
	}
	table, _ := v.get("table-type")
	up, down, ok := sudokuTableDirections(table.text)
	if !ok {
		return policyFailure(field + ".table-type")
	}
	if up != "ascii" || down != "ascii" {
		patterns, _ := v.get("custom-tables")
		single, _ := v.get("custom-table")
		if len(patterns.items) == 0 {
			if !validSudokuTable(single.text) {
				return policyFailure(field + ".custom-table")
			}
		} else {
			for _, pattern := range patterns.items {
				if !validSudokuTable(pattern.text) {
					return policyFailure(field + ".custom-tables[]")
				}
			}
		}
	}
	mode, _ := v.get("http-mask-mode")
	host, _ := v.get("http-mask-host")
	path, _ := v.get("path-root")
	mux, _ := v.get("multiplex")
	legacy, _ := v.get("http-mask-multiplex")
	if strings.TrimSpace(legacy.text) != "" {
		mux = legacy
	}
	nested, hasNested := v.get("httpmask")
	if hasNested && nested.kind != policyNull {
		if value, _ := nested.get("mode"); value.text != "" {
			mode = value
		}
		host, _ = nested.get("host")
		if value, _ := nested.get("path-root"); strings.TrimSpace(value.text) != "" {
			path = value
		}
		if value, _ := nested.get("multiplex"); strings.TrimSpace(value.text) != "" {
			mux = value
		}
	}
	switch strings.ToLower(strings.TrimSpace(mode.text)) {
	case "", "legacy", "stream", "poll", "auto", "ws":
	default:
		return policyFailure(field + ".http-mask-mode")
	}
	switch strings.ToLower(strings.TrimSpace(mux.text)) {
	case "", "off", "auto", "on":
	default:
		return policyFailure(field + ".multiplex")
	}
	if !validHTTPValue(host.text) {
		return policyFailure(field + ".http-mask-host")
	}
	if !validSudokuPathRoot(path.text) {
		return policyFailure(field + ".path-root")
	}
	return nil
}

func sudokuTableDirections(value string) (string, string, bool) {
	token := func(v string) (string, bool) {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "entropy", "prefer_entropy":
			return "entropy", true
		case "ascii", "prefer_ascii":
			return "ascii", true
		default:
			return "", false
		}
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if one, ok := token(value); ok {
		return one, one, true
	}
	if !strings.HasPrefix(value, "up_") {
		return "", "", false
	}
	a, b, ok := strings.Cut(strings.TrimPrefix(value, "up_"), "_down_")
	if !ok {
		return "", "", false
	}
	up, ok1 := token(a)
	down, ok2 := token(b)
	return up, down, ok1 && ok2
}

func validSudokuTable(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	value = strings.ToLower(strings.ReplaceAll(value, " ", ""))
	return len(value) == 8 && strings.Count(value, "x") == 2 && strings.Count(value, "p") == 2 && strings.Count(value, "v") == 4
}

func validSudokuPathRoot(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	value = strings.Trim(value, "/")
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
