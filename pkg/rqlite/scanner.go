package rqlite

// scanner.go implements row scanning logic with reflection for mapping SQL rows to Go structs and maps.

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// scanIntoDest scans multiple rows into dest (pointer to slice of structs or maps).
func scanIntoDest(rows *sql.Rows, dest any) error {
	// dest must be pointer to slice (of struct or map)
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrNotPointer
	}
	sliceVal := rv.Elem()
	if sliceVal.Kind() != reflect.Slice {
		return ErrNotSlice
	}
	elemType := sliceVal.Type().Elem()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		itemPtr := reflect.New(elemType)
		// Support map[string]any and struct
		if elemType.Kind() == reflect.Map {
			m, err := scanRowToMap(rows, cols)
			if err != nil {
				return err
			}
			sliceVal.Set(reflect.Append(sliceVal, reflect.ValueOf(m)))
			continue
		}

		if elemType.Kind() == reflect.Struct {
			if err := scanCurrentRowIntoStruct(rows, cols, itemPtr.Elem()); err != nil {
				return err
			}
			sliceVal.Set(reflect.Append(sliceVal, itemPtr.Elem()))
			continue
		}

		return fmt.Errorf("unsupported slice element type: %s", elemType.Kind())
	}
	return rows.Err()
}

// scanIntoSingle scans a single row into dest (pointer to struct or map).
func scanIntoSingle(rows *sql.Rows, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrNotPointer
	}
	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	switch rv.Elem().Kind() {
	case reflect.Map:
		m, err := scanRowToMap(rows, cols)
		if err != nil {
			return err
		}
		rv.Elem().Set(reflect.ValueOf(m))
		return nil
	case reflect.Struct:
		return scanCurrentRowIntoStruct(rows, cols, rv.Elem())
	default:
		return fmt.Errorf("unsupported dest kind: %s", rv.Elem().Kind())
	}
}

// scanRowToMap scans a single row into a map[string]any.
func scanRowToMap(rows *sql.Rows, cols []string) (map[string]any, error) {
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(cols))
	for i, c := range cols {
		out[c] = normalizeSQLValue(raw[i])
	}
	return out, nil
}

// scanCurrentRowIntoStruct scans the current row into a struct using reflection.
func scanCurrentRowIntoStruct(rows *sql.Rows, cols []string, destStruct reflect.Value) error {
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return err
	}
	fieldIndex := buildFieldIndex(destStruct.Type())
	for i, c := range cols {
		if idx, ok := fieldIndex[strings.ToLower(c)]; ok {
			field := destStruct.Field(idx)
			if field.CanSet() {
				if err := setReflectValue(field, raw[i]); err != nil {
					return fmt.Errorf("column %s: %w", c, err)
				}
			}
		}
	}
	return nil
}

// normalizeSQLValue converts SQL values to standard Go types.
func normalizeSQLValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

// buildFieldIndex creates a map of lowercase column names to field indices.
func buildFieldIndex(t reflect.Type) map[string]int {
	m := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() == false {
			continue
		}
		tag := f.Tag.Get("db")
		col := ""
		if tag != "" {
			col = strings.Split(tag, ",")[0]
		}
		if col == "" {
			col = f.Name
		}
		m[strings.ToLower(col)] = i
	}
	return m
}

// setReflectValue sets a reflect.Value from a raw SQL value.
func setReflectValue(field reflect.Value, raw any) error {
	if raw == nil {
		// leave zero value
		return nil
	}
	switch field.Kind() {
	case reflect.String:
		switch v := raw.(type) {
		case string:
			field.SetString(v)
		case []byte:
			field.SetString(string(v))
		default:
			field.SetString(fmt.Sprint(v))
		}
	case reflect.Bool:
		switch v := raw.(type) {
		case bool:
			field.SetBool(v)
		case int64:
			field.SetBool(v != 0)
		case []byte:
			s := string(v)
			field.SetBool(s == "1" || strings.EqualFold(s, "true"))
		default:
			field.SetBool(false)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch v := raw.(type) {
		case int64:
			field.SetInt(v)
		case float64:
			// RQLite/JSON returns numbers as float64
			field.SetInt(int64(v))
		case int:
			field.SetInt(int64(v))
		case []byte:
			var n int64
			fmt.Sscan(string(v), &n)
			field.SetInt(n)
		case string:
			var n int64
			fmt.Sscan(v, &n)
			field.SetInt(n)
		default:
			return fmt.Errorf("cannot convert %T to int", raw)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch v := raw.(type) {
		case int64:
			if v < 0 {
				v = 0
			}
			field.SetUint(uint64(v))
		case float64:
			// RQLite/JSON returns numbers as float64
			if v < 0 {
				v = 0
			}
			field.SetUint(uint64(v))
		case uint64:
			field.SetUint(v)
		case []byte:
			var n uint64
			fmt.Sscan(string(v), &n)
			field.SetUint(n)
		case string:
			var n uint64
			fmt.Sscan(v, &n)
			field.SetUint(n)
		default:
			return fmt.Errorf("cannot convert %T to uint", raw)
		}
	case reflect.Float32, reflect.Float64:
		switch v := raw.(type) {
		case float64:
			field.SetFloat(v)
		case []byte:
			var fv float64
			fmt.Sscan(string(v), &fv)
			field.SetFloat(fv)
		default:
			return fmt.Errorf("cannot convert %T to float", raw)
		}
	case reflect.Struct:
		// Support time.Time
		if field.Type() == reflect.TypeOf(time.Time{}) {
			switch v := raw.(type) {
			case time.Time:
				field.Set(reflect.ValueOf(v))
			case string:
				// Try RFC3339
				if tt, err := time.Parse(time.RFC3339, v); err == nil {
					field.Set(reflect.ValueOf(tt))
				}
			case []byte:
				// Try RFC3339
				if tt, err := time.Parse(time.RFC3339, string(v)); err == nil {
					field.Set(reflect.ValueOf(tt))
				}
			}
			return nil
		}
		// Support sql.NullString
		if field.Type() == reflect.TypeOf(sql.NullString{}) {
			ns := sql.NullString{}
			switch v := raw.(type) {
			case string:
				ns.String = v
				ns.Valid = true
			case []byte:
				ns.String = string(v)
				ns.Valid = true
			}
			field.Set(reflect.ValueOf(ns))
			return nil
		}
		// Support sql.NullInt64
		if field.Type() == reflect.TypeOf(sql.NullInt64{}) {
			ni := sql.NullInt64{}
			switch v := raw.(type) {
			case int64:
				ni.Int64 = v
				ni.Valid = true
			case float64:
				ni.Int64 = int64(v)
				ni.Valid = true
			case int:
				ni.Int64 = int64(v)
				ni.Valid = true
			}
			field.Set(reflect.ValueOf(ni))
			return nil
		}
		// Support sql.NullBool
		if field.Type() == reflect.TypeOf(sql.NullBool{}) {
			nb := sql.NullBool{}
			switch v := raw.(type) {
			case bool:
				nb.Bool = v
				nb.Valid = true
			case int64:
				nb.Bool = v != 0
				nb.Valid = true
			case float64:
				nb.Bool = v != 0
				nb.Valid = true
			}
			field.Set(reflect.ValueOf(nb))
			return nil
		}
		// Support sql.NullFloat64
		if field.Type() == reflect.TypeOf(sql.NullFloat64{}) {
			nf := sql.NullFloat64{}
			switch v := raw.(type) {
			case float64:
				nf.Float64 = v
				nf.Valid = true
			case int64:
				nf.Float64 = float64(v)
				nf.Valid = true
			}
			field.Set(reflect.ValueOf(nf))
			return nil
		}
		fallthrough
	case reflect.Ptr:
		// Handle pointer types (e.g. *time.Time, *string, *int)
		// nil raw is already handled above (leaves zero/nil pointer)
		elem := reflect.New(field.Type().Elem())
		if err := setReflectValue(elem.Elem(), raw); err != nil {
			return err
		}
		field.Set(elem)
		return nil
	default:
		return fmt.Errorf("unsupported dest field kind: %s", field.Kind())
	}
	return nil
}
