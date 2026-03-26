package rqlite

import (
	"reflect"
	"testing"
)

func TestQueryBuilder_SelectAll(t *testing.T) {
	qb := newQueryBuilder(nil, "users")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_SelectColumns(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Select("id", "name", "email")
	sql, args := qb.Build()

	wantSQL := "SELECT id, name, email FROM users"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_Alias(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Alias("u").Select("u.id")
	sql, args := qb.Build()

	wantSQL := "SELECT u.id FROM users AS u"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_Where(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Where("id = ?", 42)
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (id = ?)"
	wantArgs := []any{42}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestQueryBuilder_AndWhere(t *testing.T) {
	qb := newQueryBuilder(nil, "users").
		Where("age > ?", 18).
		AndWhere("status = ?", "active")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (age > ?) AND (status = ?)"
	wantArgs := []any{18, "active"}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestQueryBuilder_OrWhere(t *testing.T) {
	qb := newQueryBuilder(nil, "users").
		Where("role = ?", "admin").
		OrWhere("role = ?", "superadmin")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (role = ?) OR (role = ?)"
	wantArgs := []any{"admin", "superadmin"}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestQueryBuilder_MixedWheres(t *testing.T) {
	qb := newQueryBuilder(nil, "users").
		Where("active = ?", true).
		AndWhere("age > ?", 18).
		OrWhere("role = ?", "admin")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (active = ?) AND (age > ?) OR (role = ?)"
	wantArgs := []any{true, 18, "admin"}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestQueryBuilder_InnerJoin(t *testing.T) {
	qb := newQueryBuilder(nil, "orders").
		Select("orders.id", "users.name").
		InnerJoin("users", "orders.user_id = users.id")
	sql, args := qb.Build()

	wantSQL := "SELECT orders.id, users.name FROM orders INNER JOIN users ON orders.user_id = users.id"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_LeftJoin(t *testing.T) {
	qb := newQueryBuilder(nil, "orders").
		Select("orders.id", "users.name").
		LeftJoin("users", "orders.user_id = users.id")
	sql, args := qb.Build()

	wantSQL := "SELECT orders.id, users.name FROM orders LEFT JOIN users ON orders.user_id = users.id"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_Join(t *testing.T) {
	qb := newQueryBuilder(nil, "orders").
		Select("orders.id", "users.name").
		Join("users", "orders.user_id = users.id")
	sql, args := qb.Build()

	wantSQL := "SELECT orders.id, users.name FROM orders JOIN JOIN users ON orders.user_id = users.id"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_MultipleJoins(t *testing.T) {
	qb := newQueryBuilder(nil, "orders").
		Select("orders.id", "users.name", "products.title").
		InnerJoin("users", "orders.user_id = users.id").
		LeftJoin("products", "orders.product_id = products.id")
	sql, args := qb.Build()

	wantSQL := "SELECT orders.id, users.name, products.title FROM orders INNER JOIN users ON orders.user_id = users.id LEFT JOIN products ON orders.product_id = products.id"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_GroupBy(t *testing.T) {
	qb := newQueryBuilder(nil, "users").
		Select("status", "COUNT(*)").
		GroupBy("status")
	sql, args := qb.Build()

	wantSQL := "SELECT status, COUNT(*) FROM users GROUP BY status"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_OrderBy(t *testing.T) {
	qb := newQueryBuilder(nil, "users").OrderBy("created_at DESC")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users ORDER BY created_at DESC"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_MultipleOrderBy(t *testing.T) {
	qb := newQueryBuilder(nil, "users").OrderBy("last_name ASC", "first_name ASC")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users ORDER BY last_name ASC, first_name ASC"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_Limit(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Limit(10)
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users LIMIT 10"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_Offset(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Offset(20)
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users OFFSET 20"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_LimitAndOffset(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Limit(10).Offset(20)
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users LIMIT 10 OFFSET 20"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_ComplexQuery(t *testing.T) {
	qb := newQueryBuilder(nil, "orders").
		Alias("o").
		Select("o.id", "u.name", "o.total").
		InnerJoin("users u", "o.user_id = u.id").
		Where("o.status = ?", "completed").
		AndWhere("o.total > ?", 100).
		GroupBy("o.id", "u.name", "o.total").
		OrderBy("o.total DESC").
		Limit(10).
		Offset(5)
	sql, args := qb.Build()

	wantSQL := "SELECT o.id, u.name, o.total FROM orders AS o INNER JOIN users u ON o.user_id = u.id WHERE (o.status = ?) AND (o.total > ?) GROUP BY o.id, u.name, o.total ORDER BY o.total DESC LIMIT 10 OFFSET 5"
	wantArgs := []any{"completed", 100}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestQueryBuilder_WhereNoArgs(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Where("active = 1")
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (active = 1)"
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestQueryBuilder_MultipleArgs(t *testing.T) {
	qb := newQueryBuilder(nil, "users").Where("age BETWEEN ? AND ?", 18, 65)
	sql, args := qb.Build()

	wantSQL := "SELECT * FROM users WHERE (age BETWEEN ? AND ?)"
	wantArgs := []any{18, 65}
	if sql != wantSQL {
		t.Errorf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}
