package association

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var integer = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// Reject JSON's lossy replacement of malformed UTF16 BEFORE Go typed decoding.
func validEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] != '\\' {
				i++
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				i++
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 5
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if n >= 0xd800 && n <= 0xdbff {
				if i+5 >= len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
					return false
				}
				lo, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if err != nil || lo < 0xdc00 || lo > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}
func fields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k, v := range fields(f.Type) {
				out[k] = v
			}
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out[name] = f.Type
		}
	}
	return out
}
func strictValue(d *json.Decoder, t reflect.Type, depth int) error {
	if depth > 16 {
		return ErrContract
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return ErrContract
	}
	switch t.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return ErrContract
		}
		allowed := fields(t)
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return ErrContract
			}
			name, ok := key.(string)
			ft, known := allowed[name]
			if !ok || !known || seen[name] {
				return ErrContract
			}
			seen[name] = true
			if err := strictValue(d, ft, depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return ErrContract
		}
	case reflect.Slice:
		if token != json.Delim('[') {
			return ErrContract
		}
		count := 0
		for d.More() {
			count++
			if count > 64 {
				return ErrContract
			}
			if err := strictValue(d, t.Elem(), depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return ErrContract
		}
	case reflect.String:
		s, ok := token.(string)
		if !ok || len(s) > 4096 {
			return ErrContract
		}
	case reflect.Bool:
		if _, ok := token.(bool); !ok {
			return ErrContract
		}
	case reflect.Int, reflect.Int64, reflect.Uint64:
		n, ok := token.(json.Number)
		if !ok || !integer.MatchString(string(n)) {
			return ErrContract
		}
		u, err := strconv.ParseUint(string(n), 10, 64)
		if err != nil || u > 9007199254740991 {
			return ErrContract
		}
	default:
		return ErrContract
	}
	return nil
}
func decodeStrict(raw []byte, out any) error {
	if len(raw) > 32<<10 || !utf8.Valid(raw) || !validEscapes(raw) {
		return ErrContract
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if strictValue(d, reflect.TypeOf(out).Elem(), 0) != nil {
		return ErrContract
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrContract
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrContract
	}
	return nil
}
