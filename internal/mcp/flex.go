package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
)

// Some MCP clients (observed with the Claude/Anthropic transport) stringify
// non-string arguments — booleans arrive as "true", integers as "5", arrays as
// JSON-encoded strings. The Go SDK validates raw arguments against the tool's
// schema before unmarshalling, so a strict scalar type rejects the call with
// `has type "string", want ...`. Flex types plus WidenStringified make the
// boundary tolerant of both encodings.

// FlexBool is a bool that also accepts the JSON strings "true"/"false"
// (any strconv.ParseBool form). A JSON null decodes to false.
type FlexBool bool

// UnmarshalJSON decodes a JSON boolean, a stringified boolean, or null.
func (b *FlexBool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*b = false
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return fmt.Errorf("decoding stringified boolean: %w", err)
		}
		parsed, err := strconv.ParseBool(str)
		if err != nil {
			return fmt.Errorf("parsing boolean %q: %w", str, err)
		}
		*b = FlexBool(parsed)
		return nil
	}
	var v bool
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	*b = FlexBool(v)
	return nil
}

// Bool returns the plain bool value.
func (b FlexBool) Bool() bool { return bool(b) }

// FlexInt is an int that also accepts a JSON string containing an integer.
// A JSON null or empty string decodes to 0.
type FlexInt int

// UnmarshalJSON decodes a JSON number, a stringified integer, or null.
func (i *FlexInt) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*i = 0
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return fmt.Errorf("decoding stringified integer: %w", err)
		}
		if str == "" {
			*i = 0
			return nil
		}
		parsed, err := strconv.Atoi(str)
		if err != nil {
			return fmt.Errorf("parsing integer %q: %w", str, err)
		}
		*i = FlexInt(parsed)
		return nil
	}
	var v int
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	*i = FlexInt(v)
	return nil
}

// Int returns the plain int value.
func (i FlexInt) Int() int { return int(i) }

// FlexFloat is a float64 that also accepts a JSON string containing a number.
// A JSON null or empty string decodes to 0.
type FlexFloat float64

// UnmarshalJSON decodes a JSON number, a stringified number, or null.
func (f *FlexFloat) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*f = 0
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return fmt.Errorf("decoding stringified number: %w", err)
		}
		if str == "" {
			*f = 0
			return nil
		}
		parsed, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return fmt.Errorf("parsing number %q: %w", str, err)
		}
		*f = FlexFloat(parsed)
		return nil
	}
	var v float64
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	*f = FlexFloat(v)
	return nil
}

// Float returns the plain float64 value.
func (f FlexFloat) Float() float64 { return float64(f) }

// UnmarshalStringifiedArray decodes a JSON array of T that may also arrive as
// a JSON string containing that array (double-encoded), which some MCP clients
// send for structured arguments. A JSON null decodes to a nil slice. Callers
// wrap the error with the field name.
func UnmarshalStringifiedArray[T any](data []byte) ([]T, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return nil, fmt.Errorf("decoding stringified array: %w", err)
		}
		trimmed = []byte(str)
	}
	var arr []T
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

// InputSchema infers the JSON schema for a tool input type T and widens the
// named properties to also accept a stringified value (see WidenStringified),
// so a client that stringifies non-string arguments passes the SDK's
// pre-handler validation. T is a static type, so a failure is a programming
// error: InputSchema panics, mirroring regexp.MustCompile.
func InputSchema[T any](tool string, widen ...string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp: infer %s input schema: %v", tool, err))
	}
	WidenStringified(schema, widen...)
	return schema
}

// WidenStringified widens the named properties of schema to also accept a JSON
// string (and null), so the SDK's pre-handler validation admits a client that
// stringifies the value; the Flex* UnmarshalJSON reconstructs it either way.
// A pointer field infers as e.g. ["null","boolean"]; the scalar kind is kept
// wherever it appears. Properties not present in the schema are ignored.
func WidenStringified(schema *jsonschema.Schema, props ...string) {
	for _, name := range props {
		prop, ok := schema.Properties[name]
		if !ok {
			continue
		}
		kind := prop.Type
		if kind == "" {
			for _, t := range prop.Types {
				if t != "string" && t != "null" {
					kind = t
					break
				}
			}
		}
		prop.Type = ""
		types := []string{"string", "null"}
		if kind != "" && kind != "string" && kind != "null" {
			types = append([]string{kind}, types...)
		}
		prop.Types = types
	}
}
