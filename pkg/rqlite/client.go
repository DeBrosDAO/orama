package rqlite

// client.go defines the ORM-like interfaces and a minimal implementation over database/sql.
// It builds on the rqlite stdlib driver so it behaves like a regular SQL-backed ORM.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// TableNamer lets a struct provide its table name.
type TableNamer interface {
	TableName() string
}

// Client is the high-level ORM-like API.
type Client interface {
	// Query runs an arbitrary SELECT and scans rows into dest (pointer to slice of structs or []map[string]any).
	Query(ctx context.Context, dest any, query string, args ...any) error
	// Exec runs a write statement (INSERT/UPDATE/DELETE).
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// FindBy/FindOneBy provide simple map-based criteria filtering.
	FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error
	FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error

	// Save inserts or updates an entity (single-PK).
	Save(ctx context.Context, entity any) error
	// Remove deletes by PK (single-PK).
	Remove(ctx context.Context, entity any) error

	// Repositories (generic layer). Optional but convenient if you use Go generics.
	Repository(table string) any

	// Fluent query builder for advanced querying.
	CreateQueryBuilder(table string) *QueryBuilder

	// Tx executes a function within a transaction.
	Tx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx mirrors Client but executes within a transaction.
type Tx interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	CreateQueryBuilder(table string) *QueryBuilder

	// Optional: scoped Save/Remove inside tx
	Save(ctx context.Context, entity any) error
	Remove(ctx context.Context, entity any) error
}

// Repository provides typed entity operations for a table.
type Repository[T any] interface {
	Find(ctx context.Context, dest *[]T, criteria map[string]any, opts ...FindOption) error
	FindOne(ctx context.Context, dest *T, criteria map[string]any, opts ...FindOption) error
	Save(ctx context.Context, entity *T) error
	Remove(ctx context.Context, entity *T) error

	// Builder helpers
	Q() *QueryBuilder
}

// NewClient wires the ORM client to a *sql.DB (from your RQLiteAdapter).
func NewClient(db *sql.DB) Client {
	return &client{db: db}
}

// NewClientFromAdapter is convenient if you already created the adapter.
func NewClientFromAdapter(adapter *RQLiteAdapter) Client {
	return NewClient(adapter.GetSQLDB())
}

// client implements Client over *sql.DB.
type client struct {
	db *sql.DB
}

func (c *client) Query(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

func (c *client) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *client) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetMany(ctx, dest)
}

func (c *client) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetOne(ctx, dest)
}

func (c *client) Save(ctx context.Context, entity any) error {
	return saveEntity(ctx, c.db, entity)
}

func (c *client) Remove(ctx context.Context, entity any) error {
	return removeEntity(ctx, c.db, entity)
}

func (c *client) Repository(table string) any {
	// This returns an untyped interface since Go methods cannot have type parameters
	// Users will need to type assert the result to Repository[T]
	return func() any {
		return &repository[any]{c: c, table: table}
	}()
}

func (c *client) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(c.db, table)
}

func (c *client) Tx(ctx context.Context, fn func(tx Tx) error) error {
	sqlTx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txc := &txClient{tx: sqlTx}
	if err := fn(txc); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// txClient implements Tx over *sql.Tx.
type txClient struct {
	tx *sql.Tx
}

func (t *txClient) Query(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

func (t *txClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *txClient) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(t.tx, table)
}

func (t *txClient) Save(ctx context.Context, entity any) error {
	return saveEntity(ctx, t.tx, entity)
}

func (t *txClient) Remove(ctx context.Context, entity any) error {
	return removeEntity(ctx, t.tx, entity)
}

// executor is implemented by *sql.DB and *sql.Tx.
type executor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// QueryBuilder implements a fluent SELECT builder with joins, where, etc.
type QueryBuilder struct {
	exec    executor
	table   string
	alias   string
	selects []string

	joins  []joinClause
	wheres []whereClause

	groupBys []string
	orderBys []string
	limit    *int
	offset   *int
}

// joinClause represents INNER/LEFT/etc joins.
type joinClause struct {
	kind  string // "INNER", "LEFT", "JOIN" (default)
	table string
	on    string
}

// whereClause holds an expression and args with a conjunction.
type whereClause struct {
	conj string // "AND" or "OR"
	expr string
	args []any
}

func newQueryBuilder(exec executor, table string) *QueryBuilder {
	return &QueryBuilder{
		exec:  exec,
		table: table,
	}
}

func (qb *QueryBuilder) Select(cols ...string) *QueryBuilder {
	qb.selects = append(qb.selects, cols...)
	return qb
}

func (qb *QueryBuilder) Alias(a string) *QueryBuilder {
	qb.alias = a
	return qb
}

func (qb *QueryBuilder) Where(expr string, args ...any) *QueryBuilder {
	return qb.AndWhere(expr, args...)
}

func (qb *QueryBuilder) AndWhere(expr string, args ...any) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{conj: "AND", expr: expr, args: args})
	return qb
}

func (qb *QueryBuilder) OrWhere(expr string, args ...any) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{conj: "OR", expr: expr, args: args})
	return qb
}

func (qb *QueryBuilder) InnerJoin(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "INNER", table: table, on: on})
	return qb
}

func (qb *QueryBuilder) LeftJoin(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "LEFT", table: table, on: on})
	return qb
}

func (qb *QueryBuilder) Join(table string, on string) *QueryBuilder {
	qb.joins = append(qb.joins, joinClause{kind: "JOIN", table: table, on: on})
	return qb
}

func (qb *QueryBuilder) GroupBy(cols ...string) *QueryBuilder {
	qb.groupBys = append(qb.groupBys, cols...)
	return qb
}

func (qb *QueryBuilder) OrderBy(exprs ...string) *QueryBuilder {
	qb.orderBys = append(qb.orderBys, exprs...)
	return qb
}

func (qb *QueryBuilder) Limit(n int) *QueryBuilder {
	qb.limit = &n
	return qb
}

func (qb *QueryBuilder) Offset(n int) *QueryBuilder {
	qb.offset = &n
	return qb
}

// Build returns the SQL string and args for a SELECT.
func (qb *QueryBuilder) Build() (string, []any) {
	cols := "*"
	if len(qb.selects) > 0 {
		cols = strings.Join(qb.selects, ", ")
	}
	base := fmt.Sprintf("SELECT %s FROM %s", cols, qb.table)
	if qb.alias != "" {
		base += " AS " + qb.alias
	}

	args := make([]any, 0, 16)
	for _, j := range qb.joins {
		base += fmt.Sprintf(" %s JOIN %s ON %s", j.kind, j.table, j.on)
	}

	if len(qb.wheres) > 0 {
		base += " WHERE "
		for i, w := range qb.wheres {
			if i > 0 {
				base += " " + w.conj + " "
			}
			base += "(" + w.expr + ")"
			args = append(args, w.args...)
		}
	}

	if len(qb.groupBys) > 0 {
		base += " GROUP BY " + strings.Join(qb.groupBys, ", ")
	}
	if len(qb.orderBys) > 0 {
		base += " ORDER BY " + strings.Join(qb.orderBys, ", ")
	}
	if qb.limit != nil {
		base += fmt.Sprintf(" LIMIT %d", *qb.limit)
	}
	if qb.offset != nil {
		base += fmt.Sprintf(" OFFSET %d", *qb.offset)
	}
	return base, args
}

// GetMany executes the built query and scans into dest (pointer to slice).
func (qb *QueryBuilder) GetMany(ctx context.Context, dest any) error {
	sqlStr, args := qb.Build()
	rows, err := qb.exec.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// GetOne executes the built query and scans into dest (pointer to struct or map) with LIMIT 1.
func (qb *QueryBuilder) GetOne(ctx context.Context, dest any) error {
	limit := 1
	if qb.limit == nil {
		qb.limit = &limit
	} else if qb.limit != nil && *qb.limit > 1 {
		qb.limit = &limit
	}
	sqlStr, args := qb.Build()
	rows, err := qb.exec.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return sql.ErrNoRows
	}
	return scanIntoSingle(rows, dest)
}

// FindOption customizes Find queries.
type FindOption func(q *QueryBuilder)

func WithOrderBy(exprs ...string) FindOption {
	return func(q *QueryBuilder) { q.OrderBy(exprs...) }
}
func WithGroupBy(cols ...string) FindOption {
	return func(q *QueryBuilder) { q.GroupBy(cols...) }
}
func WithLimit(n int) FindOption {
	return func(q *QueryBuilder) { q.Limit(n) }
}
func WithOffset(n int) FindOption {
	return func(q *QueryBuilder) { q.Offset(n) }
}
func WithSelect(cols ...string) FindOption {
	return func(q *QueryBuilder) { q.Select(cols...) }
}
func WithJoin(kind, table, on string) FindOption {
	return func(q *QueryBuilder) {
		switch strings.ToUpper(kind) {
		case "INNER":
			q.InnerJoin(table, on)
		case "LEFT":
			q.LeftJoin(table, on)
		default:
			q.Join(table, on)
		}
	}
}

// repository is a generic table repository for type T.
type repository[T any] struct {
	c     *client
	table string
}

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

func (r *repository[T]) Save(ctx context.Context, entity *T) error {
	return saveEntity(ctx, r.c.db, entity)
}

func (r *repository[T]) Remove(ctx context.Context, entity *T) error {
	return removeEntity(ctx, r.c.db, entity)
}

func (r *repository[T]) Q() *QueryBuilder {
	return r.c.CreateQueryBuilder(r.table)
}

// -----------------------
// Reflection + scanning
// -----------------------

func scanIntoDest(rows *sql.Rows, dest any) error {
	// dest must be pointer to slice (of struct or map)
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("dest must be a non-nil pointer")
	}
	sliceVal := rv.Elem()
	if sliceVal.Kind() != reflect.Slice {
		return errors.New("dest must be pointer to a slice")
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

func scanIntoSingle(rows *sql.Rows, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("dest must be a non-nil pointer")
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

func normalizeSQLValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

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
		case []byte:
			var n int64
			fmt.Sscan(string(v), &n)
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
		case []byte:
			var n uint64
			fmt.Sscan(string(v), &n)
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
		// Support time.Time; extend as needed.
		if field.Type() == reflect.TypeOf(time.Time{}) {
			switch v := raw.(type) {
			case time.Time:
				field.Set(reflect.ValueOf(v))
			case []byte:
				// Try RFC3339
				if tt, err := time.Parse(time.RFC3339, string(v)); err == nil {
					field.Set(reflect.ValueOf(tt))
				}
			}
			return nil
		}
		fallthrough
	default:
		// Not supported yet
		return fmt.Errorf("unsupported dest field kind: %s", field.Kind())
	}
	return nil
}

// -----------------------
// Save/Remove (basic PK)
// -----------------------

type fieldMeta struct {
	index  int
	column string
	isPK   bool
	auto   bool
}

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

func saveEntity(ctx context.Context, exec executor, entity any) error {
	rv := reflect.ValueOf(entity)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("entity must be a non-nil pointer to struct")
	}
	ev := rv.Elem()
	if ev.Kind() != reflect.Struct {
		return errors.New("entity must point to a struct")
	}

	fields, pkMeta, hasPK := collectMeta(ev.Type())
	if !hasPK {
		return errors.New("no primary key field found (tag db:\"...,pk\" or field named ID)")
	}
	table, ok := getTableNameFromEntity(ev)
	if !ok || table == "" {
		return errors.New("unable to resolve table name; implement TableNamer or set up a repository with explicit table")
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

func removeEntity(ctx context.Context, exec executor, entity any) error {
	rv := reflect.ValueOf(entity)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("entity must be a non-nil pointer to struct")
	}
	ev := rv.Elem()
	if ev.Kind() != reflect.Struct {
		return errors.New("entity must point to a struct")
	}
	_, pkMeta, hasPK := collectMeta(ev.Type())
	if !hasPK {
		return errors.New("no primary key field found")
	}
	table, ok := getTableNameFromEntity(ev)
	if !ok || table == "" {
		return errors.New("unable to resolve table name")
	}
	pkVal := ev.Field(pkMeta.index).Interface()
	sqlStr := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, pkMeta.column)
	_, err := exec.ExecContext(ctx, sqlStr, pkVal)
	return err
}

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
