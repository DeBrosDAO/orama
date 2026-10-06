package view

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Section is one part of a node's report laid out for reading: its scalar
// fields as key/value pairs and its lists as tables.
type Section struct {
	Title  string
	Fields [][2]string
	Tables []SectionTable
}

// SectionTable is a list inside a section, one row per element.
type SectionTable struct {
	Title   string
	Headers []string
	Rows    [][]string
}

// ReportSections lays out every section of a node report, in the order the
// report declares them. It walks the report by reflection on its JSON names,
// so a field added to the report shows up here without anyone remembering to
// add it: the detail view is meant to be everything the node said.
func ReportSections(r *report.NodeReport) []Section {
	if r == nil {
		return nil
	}
	top := Section{Title: "node"}
	var out []Section
	v := reflect.ValueOf(r).Elem()
	for i := 0; i < v.NumField(); i++ {
		name, ok := jsonName(v.Type().Field(i))
		if !ok {
			continue
		}
		f := v.Field(i)
		switch {
		case isStructPtr(f):
			if !f.IsNil() {
				out = append(out, structSection(name, f.Elem()))
			}
		case isStructSlice(f):
			if f.Len() > 0 {
				out = append(out, Section{Title: name, Tables: []SectionTable{sliceTable("", f)}})
			}
		default:
			top.Fields = append(top.Fields, [2]string{name, formatValue(f)})
		}
	}
	return append([]Section{top}, out...)
}

// structSection flattens one report section.
func structSection(title string, v reflect.Value) Section {
	s := Section{Title: title}
	addStruct(&s, "", v)
	return s
}

func addStruct(s *Section, prefix string, v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		name, ok := jsonName(v.Type().Field(i))
		if !ok {
			continue
		}
		f := v.Field(i)
		switch {
		case isStructPtr(f):
			if !f.IsNil() {
				addStruct(s, prefix+name+".", f.Elem())
			}
		case isStructSlice(f):
			if f.Len() > 0 {
				s.Tables = append(s.Tables, sliceTable(prefix+name, f))
			}
		case f.Kind() == reflect.Map && isStructType(f.Type().Elem()):
			if f.Len() > 0 {
				s.Tables = append(s.Tables, mapTable(prefix+name, f))
			}
		default:
			s.Fields = append(s.Fields, [2]string{prefix + name, formatValue(f)})
		}
	}
}

// sliceTable renders a slice of structs, one row per element.
func sliceTable(title string, v reflect.Value) SectionTable {
	t := SectionTable{Title: title, Headers: structHeaders(v.Type().Elem())}
	for i := 0; i < v.Len(); i++ {
		t.Rows = append(t.Rows, structCells(v.Index(i)))
	}
	return t
}

// mapTable renders a map of structs, one row per key, keys sorted.
func mapTable(title string, v reflect.Value) SectionTable {
	t := SectionTable{Title: title, Headers: append([]string{"key"}, structHeaders(v.Type().Elem())...)}
	keys := v.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	for _, k := range keys {
		t.Rows = append(t.Rows, append([]string{fmt.Sprint(k)}, structCells(v.MapIndex(k))...))
	}
	return t
}

func structHeaders(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if name, ok := jsonName(t.Field(i)); ok {
			out = append(out, name)
		}
	}
	return out
}

func structCells(v reflect.Value) []string {
	var out []string
	for i := 0; i < v.NumField(); i++ {
		if _, ok := jsonName(v.Type().Field(i)); ok {
			out = append(out, formatValue(v.Field(i)))
		}
	}
	return out
}

// jsonName is the field's JSON name, and false for a field JSON leaves out.
func jsonName(f reflect.StructField) (string, bool) {
	if !f.IsExported() {
		return "", false
	}
	tag := strings.Split(f.Tag.Get("json"), ",")[0]
	switch tag {
	case "-":
		return "", false
	case "":
		return f.Name, true
	default:
		return tag, true
	}
}

var timeType = reflect.TypeOf(time.Time{})

func isStructType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != timeType
}

func isStructPtr(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer && isStructType(v.Type().Elem())
}

func isStructSlice(v reflect.Value) bool {
	return v.Kind() == reflect.Slice && isStructType(v.Type().Elem())
}

// formatValue renders a scalar, a slice of scalars, or anything else fmt can.
func formatValue(v reflect.Value) string {
	if v.Type() == timeType {
		t := v.Interface().(time.Time)
		if t.IsZero() {
			return unknownCell
		}
		return t.UTC().Format(time.RFC3339)
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return "yes"
		}
		return "no"
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = formatValue(v.Index(i))
		}
		return strings.Join(parts, ", ")
	case reflect.Pointer:
		if v.IsNil() {
			return unknownCell
		}
		return formatValue(v.Elem())
	default:
		return fmt.Sprint(v.Interface())
	}
}
