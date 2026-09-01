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

func TestFlexBoolUnmarshalEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{"escaped string true", `{"val": "\u0074rue"}`, true, false},
		{"escaped string false", `{"val": "fals\u0065"}`, false, false},
		{"string with spaces", `{"val": " true "}`, true, false},
		{"number two is not a bool", `{"val": 2}`, false, true},
		{"negative number", `{"val": -1}`, false, true},
		{"float", `{"val": 1.5}`, false, true},
		{"string t", `{"val": "t"}`, true, false},
	}

	for _, tc := range cases {
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

func TestFlexBoolMarshalRoundTrip(t *testing.T) {
	type payload struct {
		Val *FlexBool `json:"val,omitempty"`
	}

	yes := FlexBool(true)
	data, err := json.Marshal(payload{Val: &yes})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"val":true}` {
		t.Fatalf(`marshal = %s, want {"val":true}`, data)
	}

	if data, err = json.Marshal(payload{}); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{}` {
		t.Fatalf("marshal of an absent value = %s, want {}", data)
	}
}

func TestFlexBoolAbsentFieldStaysNil(t *testing.T) {
	var wrapper struct {
		Val *FlexBool `json:"val,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{}`), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wrapper.Val != nil {
		t.Fatalf("expected nil, got %v", *wrapper.Val)
	}
}
