// Package toolspec describes the input shape of triagent-mcp tools so the
// launcher's frontend can render input pickers + per-input
// descriptions without hand-maintaining a separate catalog.
//
// Each MCP server kind (k8s, strategies, prom, git) exposes a
// `ToolSpecs()` function that returns the list of its tools paired
// with a reflected view of each tool's input struct. The launcher
// aggregates them in-process at startup (internal/server/meta.go::
// toolCatalog) and ships the result to the frontend via metaCache.
//
// The underlying source of truth stays the Go struct + its
// `jsonschema:` tags — there's only one place to edit when an
// input gets added or its description gets reworded.
package toolspec

import (
	"reflect"
	"strings"
)

// ToolSpec describes one registered tool, with enough metadata for
// the frontend to render a name + description + input picker. The
// shape mirrors the launcher's catalog entry (internal/server/meta.go)
// so callers can serialise it directly.
type ToolSpec struct {
	Server      string      `json:"server"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Inputs      []InputSpec `json:"inputs,omitempty"`
}

// InputSpec is one field of a tool's input. Type is a coarse
// scalar/string label ("string", "integer", "boolean", "number",
// "array", "object") — enough for the frontend's input picker to
// pick the right control without reading the full JSON schema.
// Required is true when the field has no `omitempty` in its
// `json:` tag.
type InputSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// FromStruct introspects v's type — must be a struct or pointer to
// struct — and returns one InputSpec per exported field that
// participates in JSON serialisation (i.e. has a non-"-" `json:`
// tag, or no tag at all). The `jsonschema:` tag becomes the
// description; `omitempty` flips Required off.
//
// Returns nil for non-struct types so callers can pass an empty-
// input zero value (an empty struct) without special-casing.
func FromStruct(v any) []InputSpec {
	t := reflect.TypeOf(v)
	if t == nil {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []InputSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := f.Name
		required := true
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				name = parts[0]
			}
			for _, p := range parts[1:] {
				if p == "omitempty" {
					required = false
				}
			}
		}
		out = append(out, InputSpec{
			Name:        name,
			Type:        typeLabel(f.Type),
			Description: f.Tag.Get("jsonschema"),
			Required:    required,
		})
	}
	return out
}

// typeLabel maps a Go type to a coarse JSON-schema-flavoured label.
// Doesn't recurse — nested structs collapse to "object", slices to
// "array". The frontend only needs enough to pick a control kind;
// callers wanting full fidelity can read the SDK's generated JSON
// schema instead.
func typeLabel(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "string"
	}
}
