package bitbucket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FlexBool is a boolean that tolerates the shapes the Bitbucket API actually
// returns for boolean fields: a JSON boolean, a quoted boolean ("true",
// "FALSE", "1"), the numbers 0 and 1, or null. See upstream issue #234.
//
// It is a named bool rather than a struct so encoding/json marshals it back as
// a plain boolean without a custom MarshalJSON, and so *FlexBool with
// omitempty behaves exactly like the *bool it replaced.
type FlexBool bool

// Bool returns the underlying bool primitive.
func (fb *FlexBool) Bool() bool {
	if fb == nil {
		return false
	}

	return bool(*fb)
}

// UnmarshalJSON unmarshals data into a FlexBool.
func (fb *FlexBool) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*fb = FlexBool(b)
		return nil
	}

	// Decoding through encoding/json rather than trimming quotes by hand keeps
	// escape sequences working: Bitbucket may send "true".
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			*fb = false
			return nil
		}

		parsed, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("cannot unmarshal %q into FlexBool: %w", s, err)
		}
		*fb = FlexBool(parsed)

		return nil
	}

	// Numbers are deliberately narrowed to 0 and 1 rather than treating every
	// non-zero value as true, so an unexpected payload fails loudly.
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		i, err := n.Int64()
		if err != nil || (i != 0 && i != 1) {
			return fmt.Errorf("cannot unmarshal number %s into FlexBool: want 0 or 1", n)
		}
		*fb = FlexBool(i == 1)

		return nil
	}

	// A JSON null only reaches this method on a non-pointer field; for
	// *FlexBool encoding/json nils the pointer without calling UnmarshalJSON.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*fb = false
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexBool", data)
}
