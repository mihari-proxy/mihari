package service

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

type parsedExec struct {
	Binary string
	Args   []string
	Env    []string
}

type parsedUnit struct {
	parsedExec
	execStarts []string
	execKeys   int
	envFiles   []string
}

func parseSystemdUnit(raw []byte) (parsedExec, error) {
	unit, err := parseSystemdUnitFile(raw)
	if err != nil {
		return parsedExec{}, err
	}
	return unit.parsedExec, nil
}

func parseSystemdUnitFile(raw []byte) (parsedUnit, error) {
	lines, err := systemdLogicalLines(raw)
	if err != nil {
		return parsedUnit{}, err
	}
	section := ""
	var unit parsedUnit
	for _, line := range lines {
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end <= 1 || end != len(line)-1 {
				return parsedUnit{}, invalidServiceState("service definition is unsupported")
			}
			section = line[1:end]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return parsedUnit{}, invalidServiceState("service definition is unsupported")
		}
		if section != "Service" {
			continue
		}
		switch key {
		case "ExecStart":
			unit.execStarts = append(unit.execStarts, value)
			unit.execKeys++
		case "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "ExecCondition":
			unit.execKeys++
		case "Environment":
			if strings.ContainsAny(value, "$%") {
				return parsedUnit{}, invalidServiceState("service definition is unsupported")
			}
			unit.Env = append(unit.Env, value)
		case "EnvironmentFile":
			unit.envFiles = append(unit.envFiles, value)
		}
	}
	if len(unit.execStarts) > 1 || unit.execKeys > 1 {
		return parsedUnit{}, invalidServiceState("service definition is unsupported")
	}
	if len(unit.execStarts) == 1 {
		argv, err := splitExecStart(unit.execStarts[0])
		if err != nil {
			return parsedUnit{}, err
		}
		if err := rejectStrangeExec(argv); err != nil {
			return parsedUnit{}, err
		}
		unit.Binary = argv[0]
		unit.Args = argv[1:]
	}
	return unit, nil
}

func systemdLogicalLines(raw []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	var lines []string
	var pending string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if pending != "" {
			line = pending + strings.TrimLeft(line, " \t")
			pending = ""
		}
		if strings.HasSuffix(line, "\\") {
			pending = strings.TrimSuffix(line, "\\")
			continue
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if pending != "" || scanner.Err() != nil {
		return nil, invalidServiceState("service definition is unsupported")
	}
	return lines, nil
}

func splitExecStart(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, invalidServiceState("service definition is unsupported")
	}
	for value != "" && strings.ContainsRune("-@:!+", rune(value[0])) {
		return nil, invalidServiceState("service definition is unsupported")
	}
	if strings.ContainsAny(value, "$%") {
		return nil, invalidServiceState("service definition is unsupported")
	}
	var argv []string
	for value != "" {
		value = strings.TrimLeft(value, " \t")
		if value == "" {
			break
		}
		arg, rest, err := nextExecArg(value)
		if err != nil {
			return nil, err
		}
		argv = append(argv, arg)
		value = rest
	}
	if len(argv) == 0 {
		return nil, invalidServiceState("service definition is unsupported")
	}
	return argv, nil
}

func nextExecArg(value string) (string, string, error) {
	switch value[0] {
	case '\'':
		end := strings.IndexByte(value[1:], '\'')
		if end < 0 {
			return "", "", invalidServiceState("service definition is unsupported")
		}
		return value[1 : 1+end], value[2+end:], nil
	case '"':
		var b strings.Builder
		escaped := false
		for i := 1; i < len(value); i++ {
			ch := value[i]
			if escaped {
				b.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				return b.String(), value[i+1:], nil
			}
			b.WriteByte(ch)
		}
		return "", "", invalidServiceState("service definition is unsupported")
	default:
		var b strings.Builder
		escaped := false
		for i := 0; i < len(value); i++ {
			ch := value[i]
			if escaped {
				if ch == 'x' && i+2 < len(value) {
					n, err := strconv.ParseUint(value[i+1:i+3], 16, 8)
					if err != nil {
						return "", "", invalidServiceState("service definition is unsupported")
					}
					b.WriteByte(byte(n))
					i += 2
					escaped = false
					continue
				}
				b.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == ' ' || ch == '\t' {
				return b.String(), value[i:], nil
			}
			b.WriteByte(ch)
		}
		if escaped {
			return "", "", invalidServiceState("service definition is unsupported")
		}
		return b.String(), "", nil
	}
}

func rejectStrangeExec(argv []string) error {
	if (len(argv) != 2 && len(argv) != 3) || !unixAbs(argv[0]) || argv[1] != "daemon" || (len(argv) == 3 && argv[2] != "--system-service") {
		return invalidServiceState("service definition is unsupported")
	}
	base := argv[0][strings.LastIndex(argv[0], "/")+1:]
	switch base {
	case "sh", "bash", "dash", "zsh", "env", "systemctl", "python", "python3", "perl":
		return invalidServiceState("service definition is unsupported")
	}
	return nil
}

func rejectStrangeLaunchdExec(argv []string) error {
	if len(argv) == 4 && argv[3] == "--launchd-process-group" {
		return rejectStrangeExec(argv[:3])
	}
	return rejectStrangeExec(argv)
}

func parseSystemdShow(raw []byte) (map[string]string, error) {
	out := make(map[string]string, 8)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, invalidServiceState("service status is unknown")
		}
		if _, exists := out[key]; exists {
			return nil, invalidServiceState("service status is unknown")
		}
		out[key] = value
	}
	if scanner.Err() != nil {
		return nil, invalidServiceState("service status is unknown")
	}
	for _, key := range []string{"LoadState", "ActiveState", "SubState", "MainPID", "ControlGroup", "FragmentPath", "DropInPaths", "UnitFileState"} {
		if _, ok := out[key]; !ok {
			return nil, invalidServiceState("service status is unknown")
		}
	}
	return out, nil
}

func parseLaunchdPlist(raw []byte) (parsedExec, error) {
	values, err := parsePlistDictRoot(raw)
	if err != nil {
		return parsedExec{}, err
	}
	label, _ := values["Label"].(string)
	if label != serviceLabel {
		return parsedExec{}, invalidServiceState("service definition is unsupported")
	}
	var argv []string
	if args, ok := values["ProgramArguments"].([]any); ok {
		for _, item := range args {
			s, ok := item.(string)
			if !ok || s == "" {
				return parsedExec{}, invalidServiceState("service definition is unsupported")
			}
			argv = append(argv, s)
		}
	}
	if prog, ok := values["Program"].(string); ok && prog != "" {
		if len(argv) == 0 {
			argv = []string{prog}
		} else {
			argv[0] = prog
		}
	}
	if err := rejectStrangeLaunchdExec(argv); err != nil {
		return parsedExec{}, err
	}
	if value, present := values["AbandonProcessGroup"]; present {
		if abandon, ok := value.(bool); !ok || abandon {
			return parsedExec{}, invalidServiceState("service definition is unsupported")
		}
	} else if len(argv) == 4 {
		return parsedExec{}, invalidServiceState("service definition is unsupported")
	}
	var env []string
	if vars, ok := values["EnvironmentVariables"].(map[string]any); ok {
		for key, value := range vars {
			s, ok := value.(string)
			if !ok || key == "" {
				return parsedExec{}, invalidServiceState("service definition is unsupported")
			}
			env = append(env, key+"="+s)
		}
	}
	return parsedExec{Binary: argv[0], Args: argv[1:], Env: env}, nil
}

func parsePlistDictRoot(raw []byte) (map[string]any, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "plist" {
			continue
		}
		if start.Name.Local != "dict" {
			return nil, invalidServiceState("service definition is unsupported")
		}
		return parsePlistDict(dec)
	}
}

func parsePlistDict(dec *xml.Decoder) (map[string]any, error) {
	values := map[string]any{}
	key := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		switch item := tok.(type) {
		case xml.StartElement:
			if item.Name.Local == "key" {
				var next string
				if err := dec.DecodeElement(&next, &item); err != nil || next == "" {
					return nil, invalidServiceState("service definition is unsupported")
				}
				if key != "" {
					return nil, invalidServiceState("service definition is unsupported")
				}
				key = next
				continue
			}
			if key == "" {
				return nil, invalidServiceState("service definition is unsupported")
			}
			if _, exists := values[key]; exists {
				return nil, invalidServiceState("service definition is unsupported")
			}
			value, err := parsePlistValue(dec, item)
			if err != nil {
				return nil, err
			}
			values[key] = value
			key = ""
		case xml.EndElement:
			if item.Name.Local == "dict" {
				if key != "" {
					return nil, invalidServiceState("service definition is unsupported")
				}
				return values, nil
			}
		}
	}
}

func parsePlistArray(dec *xml.Decoder) ([]any, error) {
	var values []any
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		switch item := tok.(type) {
		case xml.StartElement:
			value, err := parsePlistValue(dec, item)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		case xml.EndElement:
			if item.Name.Local == "array" {
				return values, nil
			}
		}
	}
}

func parsePlistValue(dec *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "string", "integer":
		var s string
		if err := dec.DecodeElement(&s, &start); err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		return s, nil
	case "true":
		if err := dec.DecodeElement(new(struct{}), &start); err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		return true, nil
	case "false":
		if err := dec.DecodeElement(new(struct{}), &start); err != nil {
			return nil, invalidServiceState("service definition is unsupported")
		}
		return false, nil
	case "array":
		return parsePlistArray(dec)
	case "dict":
		return parsePlistDict(dec)
	default:
		return nil, invalidServiceState("service definition is unsupported")
	}
}

func parsePrintDisabled(raw []byte, label string) (bool, error) {
	text := string(raw)
	if strings.Contains(text, "gui/") {
		return false, invalidServiceState("service status is unknown")
	}
	trueKey := `"` + label + `" => true`
	falseKey := `"` + label + `" => false`
	nTrue := strings.Count(text, trueKey)
	nFalse := strings.Count(text, falseKey)
	if nTrue+nFalse != 1 {
		return false, invalidServiceState("service status is unknown")
	}
	return nTrue == 1, nil
}

func parseLaunchdPrint(result CommandResult, target string) (loaded, running bool, pid int, err error) {
	if result.ExitCode != 0 {
		if len(bytes.TrimSpace(result.Stdout)) == 0 && string(result.Stderr) == "Could not find service \""+target+"\".\n" {
			return false, false, 0, nil
		}
		return false, false, 0, invalidServiceState("service status is unknown")
	}
	text := string(result.Stdout)
	if strings.Contains(text, "gui/") {
		return false, false, 0, invalidServiceState("service status is unknown")
	}
	if !strings.Contains(text, target+" = {") && !strings.Contains(text, target+"={") {
		return false, false, 0, invalidServiceState("service status is unknown")
	}
	pid, pidOK := scanUniqueInt(text, "pid = ", `"pid" = `)
	state, stateOK := scanUniqueToken(text, "state = ")
	if !pidOK && !stateOK {
		return false, false, 0, invalidServiceState("service status is unknown")
	}
	running = pid > 0 || state == "running"
	return true, running, pid, nil
}

func scanUniqueInt(text string, prefixes ...string) (int, bool) {
	found := 0
	value := 0
	for _, prefix := range prefixes {
		start := 0
		for {
			idx := strings.Index(text[start:], prefix)
			if idx < 0 {
				break
			}
			idx += start + len(prefix)
			end := idx
			for end < len(text) && text[end] >= '0' && text[end] <= '9' {
				end++
			}
			if end == idx {
				return 0, false
			}
			n, err := strconv.Atoi(text[idx:end])
			if err != nil {
				return 0, false
			}
			found++
			value = n
			start = end
		}
	}
	return value, found == 1
}

func scanUniqueToken(text, prefix string) (string, bool) {
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return "", false
	}
	if strings.Contains(text[idx+len(prefix):], prefix) {
		return "", false
	}
	rest := text[idx+len(prefix):]
	end := strings.IndexAny(rest, "\n\r;}")
	if end < 0 {
		end = len(rest)
	}
	return strings.TrimSpace(rest[:end]), true
}
