package item_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/item"
)

// schemaPath is work/schema.json, the one owner of the record format (SPEC §8.4), relative to this
// package's directory.
func schemaPath() string { return filepath.Join("..", "..", "work", "schema.json") }

// readSchema parses work/schema.json.
func readSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(schemaPath())
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	return schema
}

// goFieldPaths returns the dotted JSON path of every field of typ ("close.effect.line"); a slice or
// a pointer adds no path element.
func goFieldPaths(typ reflect.Type, prefix string, out map[string]bool) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		out[path] = true
		goFieldPaths(f.Type, path+".", out)
	}
}

// schemaFieldPaths returns the dotted path of every property of the schema node, descending through
// the `items` of an array, and notes in open every object that does not set additionalProperties
// to false.
func schemaFieldPaths(node map[string]any, prefix string, out map[string]bool, open *[]string) {
	if items, ok := node["items"].(map[string]any); ok {
		schemaFieldPaths(items, prefix, out, open)
	}
	props, ok := node["properties"].(map[string]any)
	if !ok {
		return
	}
	if extra, set := node["additionalProperties"].(bool); !set || extra {
		*open = append(*open, strings.TrimSuffix(prefix, "."))
	}
	for name, child := range props {
		path := prefix + name
		out[path] = true
		if childNode, ok := child.(map[string]any); ok {
			schemaFieldPaths(childNode, path+".", out, open)
		}
	}
}

// diff returns the keys of a that b lacks, sorted.
func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestSchemaMatchesRecord proves work/schema.json names exactly the fields of the Record type. It
// guards against the two formats drifting: a field added to the Go type and not to the schema, a field
// left in the schema after the Go type dropped it, and a schema that lets a record carry a field
// (a time field) that Decode refuses.
func TestSchemaMatchesRecord(t *testing.T) {
	t.Parallel()
	schema := readSchema(t)

	inGo := map[string]bool{}
	goFieldPaths(reflect.TypeFor[item.Record](), "", inGo)
	inSchema := map[string]bool{}
	var open []string
	schemaFieldPaths(schema, "", inSchema, &open)

	if missing := diff(inGo, inSchema); len(missing) > 0 {
		t.Errorf("fields of Record that work/schema.json does not name: %v", missing)
	}
	if extra := diff(inSchema, inGo); len(extra) > 0 {
		t.Errorf("fields of work/schema.json that Record does not have: %v", extra)
	}
	if len(inGo) == 0 || len(inSchema) == 0 {
		t.Fatalf("compared %d Go fields with %d schema fields, want both non-empty", len(inGo), len(inSchema))
	}

	t.Run("every object is closed", func(t *testing.T) {
		t.Parallel()
		if len(open) > 0 {
			t.Errorf("objects of the schema without additionalProperties false: %q", open)
		}
	})

	t.Run("the version is the one in the Go type", func(t *testing.T) {
		t.Parallel()
		props, _ := schema["properties"].(map[string]any)
		v, _ := props["v"].(map[string]any)
		if got, ok := v["const"].(float64); !ok || got != item.SchemaVersion {
			t.Errorf("schema v const = %v, want %d", v["const"], item.SchemaVersion)
		}
	})

	t.Run("the type and status values are the ones in the Go type", func(t *testing.T) {
		t.Parallel()
		props, _ := schema["properties"].(map[string]any)
		for name, want := range map[string][]string{
			"type":   toStrings(item.Types()),
			"status": toStrings(item.Statuses()),
		} {
			node, _ := props[name].(map[string]any)
			enum, _ := node["enum"].([]any)
			var got []string
			for _, e := range enum {
				s, _ := e.(string)
				got = append(got, s)
			}
			if !slices.Equal(got, want) {
				t.Errorf("schema %s enum = %v, want %v", name, got, want)
			}
		}
	})
}

// toStrings converts a typed value set to its string values.
func toStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}
