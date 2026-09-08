package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"
)

// decodeInstallationJSON decodes one bounded JSON object using exact field
// names. Every struct field is required; null is accepted only for pointers.
func decodeInstallationJSON(reader io.Reader, max int, dest any) (map[string]bool, error) {
	if reader == nil || max <= 0 || dest == nil {
		return nil, os.ErrInvalid
	}
	typeOfDest := reflect.TypeOf(dest)
	valueOfDest := reflect.ValueOf(dest)
	if typeOfDest.Kind() != reflect.Pointer || valueOfDest.IsNil() || typeOfDest.Elem().Kind() != reflect.Struct {
		return nil, os.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(max)+1))
	if err != nil || len(data) > max || !utf8.Valid(data) {
		return nil, os.ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	keys, err := uniqueInstallationObject(dec)
	if err != nil {
		return nil, os.ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, os.ErrInvalid
	}
	if err := validateInstallationJSONShape(data, typeOfDest.Elem()); err != nil {
		return nil, os.ErrInvalid
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return nil, os.ErrInvalid
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, os.ErrInvalid
	}
	return keys, nil
}

func uniqueInstallationObject(dec *json.Decoder) (map[string]bool, error) {
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, os.ErrInvalid
	}
	keys := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		name, ok := key.(string)
		if err != nil || !ok || keys[name] {
			return nil, os.ErrInvalid
		}
		keys[name] = true
		if err := uniqueInstallationValue(dec, 1); err != nil {
			return nil, err
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, os.ErrInvalid
	}
	return keys, nil
}

func uniqueInstallationValue(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return os.ErrInvalid
	}
	token, err := dec.Token()
	if err != nil {
		return os.ErrInvalid
	}
	if token == nil {
		return nil
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return os.ErrInvalid
			}
			seen[name] = true
			if err := uniqueInstallationValue(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return os.ErrInvalid
		}
	case json.Delim('['):
		for dec.More() {
			if err := uniqueInstallationValue(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return os.ErrInvalid
		}
	}
	return nil
}

func validateInstallationJSONShape(raw []byte, typ reflect.Type) error {
	raw = bytes.TrimSpace(raw)
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		return validateInstallationJSONShape(raw, typ.Elem())
	}
	if bytes.Equal(raw, []byte("null")) {
		return os.ErrInvalid
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return os.ErrInvalid
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		if len(object) != len(fields) {
			return os.ErrInvalid
		}
		for name, fieldType := range fields {
			value, ok := object[name]
			if !ok {
				return os.ErrInvalid
			}
			if err := validateInstallationJSONShape(value, fieldType); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || items == nil {
			return os.ErrInvalid
		}
		for _, item := range items {
			if err := validateInstallationJSONShape(item, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
