package bitbucket

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FlexBool is a boolean that can be deserialized from JSON booleans, strings ("true", "false", "1", "0"), numbers, or null.
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
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*fb = false
		return nil
	}

	// Direct bool literal
	if bytes.Equal(trimmed, []byte("true")) {
		*fb = true
		return nil
	}
	if bytes.Equal(trimmed, []byte("false")) {
		*fb = false
		return nil
	}

	// String literal
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		s := strings.TrimSpace(string(trimmed[1 : len(trimmed)-1]))
		if s == "" {
			*fb = false
			return nil
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("cannot unmarshal %q into FlexBool: %w", s, err)
		}
		*fb = FlexBool(b)
		return nil
	}

	// Number literal
	if b, err := strconv.ParseBool(string(trimmed)); err == nil {
		*fb = FlexBool(b)
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexBool", string(trimmed))
}
