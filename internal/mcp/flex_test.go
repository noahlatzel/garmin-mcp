package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestFlexBoolUnmarshal(t *testing.T) {
	cases := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{`true`, true, false},
		{`false`, false, false},
		{`"true"`, true, false},
		{`"false"`, false, false},
		{`"1"`, true, false},
		{`"0"`, false, false},
		{`null`, false, false},
		{`"yes"`, false, true},
		{`5`, false, true},
	}
	for _, tc := range cases {
		var b FlexBool
		err := json.Unmarshal([]byte(tc.in), &b)
		if tc.wantErr {
			if err == nil {
				t.Errorf("FlexBool(%s): want error, got %v", tc.in, b)
			}
			continue
		}
		if err != nil {
			t.Errorf("FlexBool(%s): unexpected error: %v", tc.in, err)
			continue
		}
		if b.Bool() != tc.want {
			t.Errorf("FlexBool(%s) = %v, want %v", tc.in, b.Bool(), tc.want)
		}
	}
}

func TestFlexIntUnmarshal(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{`7`, 7, false},
		{`"42"`, 42, false},
		{`"-3"`, -3, false},
		{`""`, 0, false},
		{`null`, 0, false},
		{`"seven"`, 0, true},
		{`true`, 0, true},
	}
	for _, tc := range cases {
		var i FlexInt
		err := json.Unmarshal([]byte(tc.in), &i)
		if tc.wantErr {
			if err == nil {
				t.Errorf("FlexInt(%s): want error, got %v", tc.in, i)
			}
			continue
		}
		if err != nil {
			t.Errorf("FlexInt(%s): unexpected error: %v", tc.in, err)
			continue
		}
		if i.Int() != tc.want {
			t.Errorf("FlexInt(%s) = %d, want %d", tc.in, i.Int(), tc.want)
		}
	}
}

func TestWidenStringified(t *testing.T) {
	type input struct {
		Confirm FlexBool  `json:"confirm,omitempty"`
		Limit   FlexInt   `json:"limit,omitempty"`
		Rest    *FlexBool `json:"rest,omitempty"` // pointer: infers as ["null","boolean"]
		Name    string    `json:"name,omitempty"`
	}
	schema, err := jsonschema.For[input](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	WidenStringified(schema, "confirm", "limit", "rest", "missing")

	want := map[string][]string{
		"confirm": {"boolean", "string", "null"},
		"limit":   {"integer", "string", "null"},
		"rest":    {"boolean", "string", "null"},
	}
	for name, types := range want {
		prop := schema.Properties[name]
		if prop == nil {
			t.Fatalf("property %q missing from schema", name)
		}
		if prop.Type != "" {
			t.Errorf("%s: Type = %q, want empty", name, prop.Type)
		}
		if len(prop.Types) != len(types) {
			t.Fatalf("%s: Types = %v, want %v", name, prop.Types, types)
		}
		for i := range types {
			if prop.Types[i] != types[i] {
				t.Errorf("%s: Types = %v, want %v", name, prop.Types, types)
				break
			}
		}
	}
	if name := schema.Properties["name"]; name.Type != "string" {
		t.Errorf("name property was modified: Type = %q, Types = %v", name.Type, name.Types)
	}
}

func TestUnmarshalStringifiedArray(t *testing.T) {
	type step struct {
		Name string  `json:"name"`
		Reps FlexInt `json:"reps,omitempty"`
	}
	native := `[{"name":"a","reps":5},{"name":"b"}]`
	stringified := `"[{\"name\":\"a\",\"reps\":\"5\"},{\"name\":\"b\"}]"`

	for _, in := range []string{native, stringified} {
		got, err := UnmarshalStringifiedArray[step]([]byte(in))
		if err != nil {
			t.Fatalf("UnmarshalStringifiedArray(%s): %v", in, err)
		}
		if len(got) != 2 || got[0].Name != "a" || got[0].Reps.Int() != 5 || got[1].Name != "b" {
			t.Errorf("UnmarshalStringifiedArray(%s) = %+v", in, got)
		}
	}

	if got, err := UnmarshalStringifiedArray[step]([]byte(`null`)); err != nil || got != nil {
		t.Errorf("null: got %v, %v; want nil, nil", got, err)
	}
	if _, err := UnmarshalStringifiedArray[step]([]byte(`"not json"`)); err == nil {
		t.Error("stringified non-JSON: want error, got nil")
	}
	if _, err := UnmarshalStringifiedArray[step]([]byte(`{"name":"a"}`)); err == nil {
		t.Error("bare object: want error, got nil")
	}
}

func TestInputSchema(t *testing.T) {
	type input struct {
		ID      string   `json:"id"`
		Confirm FlexBool `json:"confirm,omitempty"`
	}
	schema := InputSchema[input]("test_tool", "confirm")
	confirm := schema.Properties["confirm"]
	if confirm == nil || confirm.Type != "" || len(confirm.Types) != 3 || confirm.Types[0] != "boolean" {
		t.Errorf("confirm not widened: %+v", confirm)
	}
	if id := schema.Properties["id"]; id == nil || id.Type != "string" {
		t.Errorf("id property changed unexpectedly: %+v", id)
	}
}
