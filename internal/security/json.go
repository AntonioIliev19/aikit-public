package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// CheckJSON rejects duplicate object keys, which encoding/json otherwise silently
// replaces. Input size must already be bounded by the caller.
func CheckJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return ErrDenied
		}
		t, err := d.Token()
		if err != nil {
			return ErrDenied
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		if delim != '{' && delim != '[' {
			return ErrDenied
		}
		seen := map[string]bool{}
		for d.More() {
			if delim == '{' {
				key, err := d.Token()
				if err != nil {
					return ErrDenied
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return ErrDenied
				}
				seen[s] = true
			}
			if err := value(depth + 1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
			return ErrDenied
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return ErrDenied
	}
	return nil
}
