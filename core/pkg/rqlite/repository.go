package rqlite

// repository.go implements the generic Repository[T] pattern for typed entity operations.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// repository is a generic table repository for type T.
type repository[T any] struct {
	c     *client
	table string
}

// Find queries entities matching criteria and returns them in dest.
func (r *repository[T]) Find(ctx context.Context, dest *[]T, criteria map[string]any, opts ...FindOption) error {
	qb := r.c.CreateQueryBuilder(r.table)
	for k, v := range criteria {
		qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetMany(ctx, dest)
}

// FindOne queries a single entity matching criteria.
func (r *repository[T]) FindOne(ctx context.Context, dest *T, criteria map[string]any, opts ...FindOption) error {
	qb := r.c.CreateQueryBuilder(r.table)
	for k, v := range criteria {
		qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetOne(ctx, dest)
}

// Save inserts or updates the entity.
func (r *repository[T]) Save(ctx context.Context, entity *T) error {
	return saveEntity(ctx, r.c.db, entity)
}

// Remove deletes the entity by primary key.
func (r *repository[T]) Remove(ctx context.Context, entity *T) error {
	return removeEntity(ctx, r.c.db, entity)
}

// Q returns a QueryBuilder for this repository's table.
func (r *repository[T]) Q() *QueryBuilder {
	return r.c.CreateQueryBuilder(r.table)
}

// collectMeta extracts field metadata from a struct type.
func collectMeta(t reflect.Type) (fields []fieldMeta, pk fieldMeta, hasPK bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("db")
		if tag == "-" {
			continue
		}
		opts := strings.Split(tag, ",")
		col := opts[0]
		if col == "" {
			col = f.Name
		}
		meta := fieldMeta{index: i, column: col}
		for _, o := range opts[1:] {
			switch strings.ToLower(strings.TrimSpace(o)) {
			case "pk":
				meta.isPK = true
			case "auto", "autoincrement":
				meta.auto = true
			}
		}
		// If not tagged as pk, fallback to field name "ID"
		if !meta.isPK && f.Name == "ID" {
			meta.isPK = true
			if col == "" {
				meta.column = "id"
			}
		}
		fields = append(fields, meta)
		if meta.isPK {
			pk = meta
			hasPK = true
		}
	}
	return
}

// getTableNameFromEntity resolves the table name from an entity.
func getTableNameFromEntity(v reflect.Value) (string, bool) {
	// If entity implements TableNamer
	if v.CanInterface() {
		if tn, ok := v.Interface().(TableNamer); ok {
			return tn.TableName(), true
		}
	}
	// Fallback: very naive pluralization (append 's')
	typ := v.Type()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Struct {
		return strings.ToLower(typ.Name()) + "s", true
	}
	return "", false
}

// saveEntity inserts or updates an entity based on its primary key value.
func saveEntity(ctx context.Context, exec executor, entity any) error {
	rv := reflect.ValueOf(entity)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrEntityMustBePointer
	}
	ev := rv.Elem()
	if ev.Kind() != reflect.Struct {
		return ErrNotStruct
	}

	fields, pkMeta, hasPK := collectMeta(ev.Type())
	if !hasPK {
		return ErrNoPrimaryKey
	}
	table, ok := getTableNameFromEntity(ev)
	if !ok || table == "" {
		return ErrNoTableName
	}

	// Build lists
	cols := make([]string, 0, len(fields))
	vals := make([]any, 0, len(fields))
	setParts := make([]string, 0, len(fields))

	var pkVal any
	var pkIsZero bool

	for _, fm := range fields {
		f := ev.Field(fm.index)
		if fm.isPK {
			pkVal = f.Interface()
			pkIsZero = isZeroValue(f)
			continue
		}
		cols = append(cols, fm.column)
		vals = append(vals, f.Interface())
		setParts = append(setParts, fmt.Sprintf("%s = ?", fm.column))
	}

	if pkIsZero {
		// INSERT
		placeholders := strings.Repeat("?,", len(cols))
		if len(placeholders) > 0 {
			placeholders = placeholders[:len(placeholders)-1]
		}
		sqlStr := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), placeholders)
		res, err := exec.ExecContext(ctx, sqlStr, vals...)
		if err != nil {
			return err
		}
		// Set auto ID if needed
		if pkMeta.auto {
			if id, err := res.LastInsertId(); err == nil {
				ev.Field(pkMeta.index).SetInt(id)
			}
		}
		return nil
	}

	// UPDATE ... WHERE pk = ?
	sqlStr := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ?", table, strings.Join(setParts, ", "), pkMeta.column)
	valsWithPK := append(vals, pkVal)
	_, err := exec.ExecContext(ctx, sqlStr, valsWithPK...)
	return err
}

// removeEntity deletes an entity by its primary key.
func removeEntity(ctx context.Context, exec executor, entity any) error {
	rv := reflect.ValueOf(entity)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrEntityMustBePointer
	}
	ev := rv.Elem()
	if ev.Kind() != reflect.Struct {
		return ErrNotStruct
	}
	_, pkMeta, hasPK := collectMeta(ev.Type())
	if !hasPK {
		return ErrNoPrimaryKey
	}
	table, ok := getTableNameFromEntity(ev)
	if !ok || table == "" {
		return ErrNoTableName
	}
	pkVal := ev.Field(pkMeta.index).Interface()
	sqlStr := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, pkMeta.column)
	_, err := exec.ExecContext(ctx, sqlStr, pkVal)
	return err
}

// isZeroValue checks if a reflect.Value is its zero value.
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Bool:
		return v.Bool() == false
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Struct:
		// Special-case time.Time
		if v.Type() == reflect.TypeOf(time.Time{}) {
			t := v.Interface().(time.Time)
			return t.IsZero()
		}
		zero := reflect.Zero(v.Type())
		return reflect.DeepEqual(v.Interface(), zero.Interface())
	default:
		return false
	}
}
