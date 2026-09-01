package bitbucket

import (
	"encoding/json"
	"testing"
)

func TestFlexBoolUnmarshal(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{"native true", `{"val": true}`, true, false},
		{"native false", `{"val": false}`, false, false},
		{"string true", `{"val": "true"}`, true, false},
		{"string false", `{"val": "false"}`, false, false},
		{"string uppercase true", `{"val": "TRUE"}`, true, false},
		{"string uppercase false", `{"val": "FALSE"}`, false, false},
		{"string 1", `{"val": "1"}`, true, false},
		{"string 0", `{"val": "0"}`, false, false},
		{"numeric 1", `{"val": 1}`, true, false},
		{"numeric 0", `{"val": 0}`, false, false},
		{"null", `{"val": null}`, false, false},
		{"empty string", `{"val": ""}`, false, false},
		{"invalid string", `{"val": "invalid"}`, false, true},
		{"invalid array", `{"val": []}`, false, true},
		{"invalid object", `{"val": {}}`, false, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var wrapper struct {
				Val FlexBool `json:"val"`
			}
			err := json.Unmarshal([]byte(tc.input), &wrapper)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error: %v, got: %v", tc.wantErr, err)
			}
			if !tc.wantErr && wrapper.Val.Bool() != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, wrapper.Val.Bool())
			}
		})
	}
}

func TestFlexBoolPointer(t *testing.T) {
	type wrapper struct {
		Val *FlexBool `json:"val,omitempty"`
	}

	var w wrapper
	if err := json.Unmarshal([]byte(`{"val": "false"}`), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Val == nil {
		t.Fatal("expected pointer to be non-nil")
	}
	if w.Val.Bool() != false {
		t.Fatalf("expected false, got %v", w.Val.Bool())
	}
}
