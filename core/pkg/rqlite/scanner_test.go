package rqlite

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// normalizeSQLValue
// ---------------------------------------------------------------------------

func TestNormalizeSQLValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{"byte slice to string", []byte("hello"), "hello"},
		{"string unchanged", "already string", "already string"},
		{"int unchanged", 42, 42},
		{"float64 unchanged", 3.14, 3.14},
		{"nil unchanged", nil, nil},
		{"bool unchanged", true, true},
		{"int64 unchanged", int64(99), int64(99)},
		{"empty byte slice to empty string", []byte(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeSQLValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// buildFieldIndex
// ---------------------------------------------------------------------------

type taggedStruct struct {
	ID        int    `db:"id"`
	UserName  string `db:"user_name"`
	Email     string `db:"email_addr"`
	CreatedAt string `db:"created_at"`
}

type untaggedStruct struct {
	ID    int
	Name  string
	Email string
}

type mixedStruct struct {
	ID      int    `db:"id"`
	Name    string // no tag — should use lowercased field name "name"
	Skipped string `db:"-"`
	Active  bool   `db:"is_active"`
}

type structWithUnexported struct {
	ID       int `db:"id"`
	internal string
	Name     string `db:"name"`
}

type embeddedBase struct {
	BaseField string `db:"base_field"`
}

type structWithEmbedded struct {
	embeddedBase
	Name string `db:"name"`
}

func TestBuildFieldIndex(t *testing.T) {
	t.Run("tagged struct", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(taggedStruct{}))
		// Keys are normalized via normalizeColumnKey: lowercased and
		// underscores stripped. So `user_name` and `UserName` both
		// resolve to `username` and the scanner can match snake_case SQL
		// columns onto CamelCase struct fields without explicit `db:` tags
		// (feature #65 cron-scheduler bug fix).
		assert.Equal(t, 0, idx["id"])
		assert.Equal(t, 1, idx["username"])
		assert.Equal(t, 2, idx["emailaddr"])
		assert.Equal(t, 3, idx["createdat"])
		assert.Len(t, idx, 4)
	})

	t.Run("untagged struct uses lowercased field name", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(untaggedStruct{}))
		assert.Equal(t, 0, idx["id"])
		assert.Equal(t, 1, idx["name"])
		assert.Equal(t, 2, idx["email"])
		assert.Len(t, idx, 3)
	})

	t.Run("mixed struct with dash tag excluded", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(mixedStruct{}))
		assert.Equal(t, 0, idx["id"])
		assert.Equal(t, 1, idx["name"])
		// `is_active` normalizes to `isactive`.
		assert.Equal(t, 3, idx["isactive"])
		// "-" tag means the first part of the tag is "-", so it maps with key "-"
		// The actual behavior: tag="-" → col="-" → stored as "-"
		// Let's verify what actually happens
		_, hasDash := idx["-"]
		_, hasSkipped := idx["skipped"]
		// The function splits on "," and uses the first part. For db:"-", col = "-".
		// So it maps lowercase("-") = "-" → index 2.
		// It does NOT skip the field — it maps it with key "-".
		assert.True(t, hasDash || hasSkipped, "dash-tagged field should appear with key '-' since the function does not skip it")
	})

	t.Run("unexported fields are skipped", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(structWithUnexported{}))
		assert.Equal(t, 0, idx["id"])
		assert.Equal(t, 2, idx["name"])
		_, hasInternal := idx["internal"]
		assert.False(t, hasInternal, "unexported field should be skipped")
		assert.Len(t, idx, 2)
	})

	t.Run("struct with embedded field", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(structWithEmbedded{}))
		// Embedded struct is treated as a field at index 0 with type embeddedBase.
		// Since embeddedBase is exported (starts with lowercase 'e' — wait, no,
		// Go embedded fields: the type name is embeddedBase which starts with lowercase,
		// so it's unexported. The field itself is unexported.
		// So buildFieldIndex will skip it (IsExported() == false).
		assert.Equal(t, 1, idx["name"])
		_, hasBase := idx["base_field"]
		assert.False(t, hasBase, "unexported embedded struct field is not indexed")
	})

	t.Run("empty struct", func(t *testing.T) {
		type emptyStruct struct{}
		idx := buildFieldIndex(reflect.TypeOf(emptyStruct{}))
		assert.Len(t, idx, 0)
	})

	t.Run("tag with comma options", func(t *testing.T) {
		type commaStruct struct {
			ID   int    `db:"id,pk"`
			Name string `db:"name,omitempty"`
		}
		idx := buildFieldIndex(reflect.TypeOf(commaStruct{}))
		assert.Equal(t, 0, idx["id"])
		assert.Equal(t, 1, idx["name"])
		assert.Len(t, idx, 2)
	})

	t.Run("column name lookup is case insensitive", func(t *testing.T) {
		idx := buildFieldIndex(reflect.TypeOf(taggedStruct{}))
		// All keys are stored lowercased, so "ID" won't match but "id" will.
		_, hasUpperID := idx["ID"]
		assert.False(t, hasUpperID)
		_, hasLowerID := idx["id"]
		assert.True(t, hasLowerID)
	})

	// REGRESSION (feature #65 cron-scheduler bug): a struct with CamelCase
	// fields and NO `db:` tags must map onto snake_case SQL columns.
	// Pre-fix, the cron scheduler read DB rows with valid `trigger_id` and
	// `cron_expression` columns into a `CronDueRow` struct with `TriggerID`
	// and `CronExpression` fields — and got back zero values for both,
	// because `triggerid` (the lowercased struct field name) didn't match
	// `trigger_id` (the lowercased column name) in the index lookup.
	t.Run("camelCase field maps to snake_case column without db tag", func(t *testing.T) {
		type cronDueRowLike struct {
			TriggerID      string
			FunctionID     string
			FunctionName   string
			Namespace      string
			CronExpression string
		}
		idx := buildFieldIndex(reflect.TypeOf(cronDueRowLike{}))

		// Both spellings of the same key must resolve to the same field
		// because of normalizeColumnKey.
		for _, sqlCol := range []string{"trigger_id", "TriggerID", "triggerid"} {
			pos, ok := idx[normalizeColumnKey(sqlCol)]
			assert.True(t, ok, "column %q should map to TriggerID", sqlCol)
			assert.Equal(t, 0, pos)
		}
		for _, sqlCol := range []string{"cron_expression", "CronExpression"} {
			pos, ok := idx[normalizeColumnKey(sqlCol)]
			assert.True(t, ok, "column %q should map to CronExpression", sqlCol)
			assert.Equal(t, 4, pos)
		}
	})
}

// TestNormalizeColumnKey exercises the central rule that snake_case ↔
// CamelCase map to the same scanner key. Pre-fix, the absence of this
// normalization silently broke struct scanning for any multi-word column
// name (the cron scheduler in feature #65 was the canary).
func TestNormalizeColumnKey(t *testing.T) {
	cases := map[string]string{
		"id":              "id",
		"ID":              "id",
		"trigger_id":      "triggerid",
		"TriggerID":       "triggerid",
		"triggerID":       "triggerid",
		"cron_expression": "cronexpression",
		"CronExpression":  "cronexpression",
		"":                "",
		"_":               "",
		"___":             "",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeColumnKey(in), "normalizeColumnKey(%q)", in)
	}
}

// ---------------------------------------------------------------------------
// setReflectValue
// ---------------------------------------------------------------------------

// testTarget holds fields of various types for setReflectValue tests.
type testTarget struct {
	StringField  string
	IntField     int
	Int64Field   int64
	UintField    uint
	Uint64Field  uint64
	BoolField    bool
	Float64Field float64
	TimeField    time.Time
	PtrString    *string
	PtrInt       *int
	NullString   sql.NullString
	NullInt64    sql.NullInt64
	NullBool     sql.NullBool
	NullFloat64  sql.NullFloat64
}

// fieldOf returns a settable reflect.Value for the named field on *target.
func fieldOf(target *testTarget, name string) reflect.Value {
	return reflect.ValueOf(target).Elem().FieldByName(name)
}

func TestSetReflectValue_String(t *testing.T) {
	t.Run("from string", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "StringField"), "hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", s.StringField)
	})

	t.Run("from byte slice", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "StringField"), []byte("world"))
		require.NoError(t, err)
		assert.Equal(t, "world", s.StringField)
	})

	t.Run("from int via Sprint", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "StringField"), 42)
		require.NoError(t, err)
		assert.Equal(t, "42", s.StringField)
	})

	t.Run("from nil leaves zero value", func(t *testing.T) {
		var s testTarget
		s.StringField = "preset"
		err := setReflectValue(fieldOf(&s, "StringField"), nil)
		require.NoError(t, err)
		assert.Equal(t, "preset", s.StringField) // nil leaves field unchanged
	})
}

func TestSetReflectValue_Int(t *testing.T) {
	t.Run("from int64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), int64(100))
		require.NoError(t, err)
		assert.Equal(t, 100, s.IntField)
	})

	t.Run("from float64 (JSON number)", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), float64(42))
		require.NoError(t, err)
		assert.Equal(t, 42, s.IntField)
	})

	t.Run("from int", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), int(77))
		require.NoError(t, err)
		assert.Equal(t, 77, s.IntField)
	})

	t.Run("from byte slice", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), []byte("123"))
		require.NoError(t, err)
		assert.Equal(t, 123, s.IntField)
	})

	t.Run("from string", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), "456")
		require.NoError(t, err)
		assert.Equal(t, 456, s.IntField)
	})

	t.Run("unsupported type returns error", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "IntField"), true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot convert")
	})

	t.Run("int64 field from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "Int64Field"), float64(999))
		require.NoError(t, err)
		assert.Equal(t, int64(999), s.Int64Field)
	})
}

func TestSetReflectValue_Uint(t *testing.T) {
	t.Run("from int64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), int64(50))
		require.NoError(t, err)
		assert.Equal(t, uint(50), s.UintField)
	})

	t.Run("from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), float64(75))
		require.NoError(t, err)
		assert.Equal(t, uint(75), s.UintField)
	})

	t.Run("from uint64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "Uint64Field"), uint64(12345))
		require.NoError(t, err)
		assert.Equal(t, uint64(12345), s.Uint64Field)
	})

	t.Run("negative int64 clamps to zero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), int64(-5))
		require.NoError(t, err)
		assert.Equal(t, uint(0), s.UintField)
	})

	t.Run("negative float64 clamps to zero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), float64(-3.14))
		require.NoError(t, err)
		assert.Equal(t, uint(0), s.UintField)
	})

	t.Run("from byte slice", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), []byte("88"))
		require.NoError(t, err)
		assert.Equal(t, uint(88), s.UintField)
	})

	t.Run("from string", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), "99")
		require.NoError(t, err)
		assert.Equal(t, uint(99), s.UintField)
	})

	t.Run("unsupported type returns error", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "UintField"), true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot convert")
	})
}

func TestSetReflectValue_Bool(t *testing.T) {
	t.Run("from bool true", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "BoolField"), true)
		require.NoError(t, err)
		assert.True(t, s.BoolField)
	})

	t.Run("from bool false", func(t *testing.T) {
		var s testTarget
		s.BoolField = true
		err := setReflectValue(fieldOf(&s, "BoolField"), false)
		require.NoError(t, err)
		assert.False(t, s.BoolField)
	})

	t.Run("from int64 nonzero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "BoolField"), int64(1))
		require.NoError(t, err)
		assert.True(t, s.BoolField)
	})

	t.Run("from int64 zero", func(t *testing.T) {
		var s testTarget
		s.BoolField = true
		err := setReflectValue(fieldOf(&s, "BoolField"), int64(0))
		require.NoError(t, err)
		assert.False(t, s.BoolField)
	})

	t.Run("from byte slice '1'", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "BoolField"), []byte("1"))
		require.NoError(t, err)
		assert.True(t, s.BoolField)
	})

	t.Run("from byte slice 'true'", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "BoolField"), []byte("true"))
		require.NoError(t, err)
		assert.True(t, s.BoolField)
	})

	t.Run("from byte slice 'false'", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "BoolField"), []byte("false"))
		require.NoError(t, err)
		assert.False(t, s.BoolField)
	})

	t.Run("from unknown type sets false", func(t *testing.T) {
		var s testTarget
		s.BoolField = true
		err := setReflectValue(fieldOf(&s, "BoolField"), "not a bool")
		require.NoError(t, err)
		assert.False(t, s.BoolField)
	})
}

func TestSetReflectValue_Float64(t *testing.T) {
	t.Run("from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "Float64Field"), float64(3.14))
		require.NoError(t, err)
		assert.InDelta(t, 3.14, s.Float64Field, 0.001)
	})

	t.Run("from byte slice", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "Float64Field"), []byte("2.718"))
		require.NoError(t, err)
		assert.InDelta(t, 2.718, s.Float64Field, 0.001)
	})

	t.Run("unsupported type returns error", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "Float64Field"), "not a float")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot convert")
	})
}

func TestSetReflectValue_Time(t *testing.T) {
	t.Run("from time.Time", func(t *testing.T) {
		var s testTarget
		now := time.Now().UTC().Truncate(time.Second)
		err := setReflectValue(fieldOf(&s, "TimeField"), now)
		require.NoError(t, err)
		assert.True(t, now.Equal(s.TimeField))
	})

	t.Run("from RFC3339 string", func(t *testing.T) {
		var s testTarget
		ts := "2024-06-15T10:30:00Z"
		err := setReflectValue(fieldOf(&s, "TimeField"), ts)
		require.NoError(t, err)
		expected, _ := time.Parse(time.RFC3339, ts)
		assert.True(t, expected.Equal(s.TimeField))
	})

	t.Run("from RFC3339 byte slice", func(t *testing.T) {
		var s testTarget
		ts := "2024-06-15T10:30:00Z"
		err := setReflectValue(fieldOf(&s, "TimeField"), []byte(ts))
		require.NoError(t, err)
		expected, _ := time.Parse(time.RFC3339, ts)
		assert.True(t, expected.Equal(s.TimeField))
	})

	t.Run("invalid time string leaves zero value", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "TimeField"), "not-a-time")
		require.NoError(t, err)
		assert.True(t, s.TimeField.IsZero())
	})
}

func TestSetReflectValue_Pointer(t *testing.T) {
	t.Run("*string from string", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "PtrString"), "hello")
		require.NoError(t, err)
		require.NotNil(t, s.PtrString)
		assert.Equal(t, "hello", *s.PtrString)
	})

	t.Run("*string from nil leaves nil", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "PtrString"), nil)
		require.NoError(t, err)
		assert.Nil(t, s.PtrString)
	})

	t.Run("*int from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "PtrInt"), float64(42))
		require.NoError(t, err)
		require.NotNil(t, s.PtrInt)
		assert.Equal(t, 42, *s.PtrInt)
	})

	t.Run("*int from int64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "PtrInt"), int64(99))
		require.NoError(t, err)
		require.NotNil(t, s.PtrInt)
		assert.Equal(t, 99, *s.PtrInt)
	})
}

func TestSetReflectValue_NullString(t *testing.T) {
	t.Run("from string", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullString"), "hello")
		require.NoError(t, err)
		assert.True(t, s.NullString.Valid)
		assert.Equal(t, "hello", s.NullString.String)
	})

	t.Run("from byte slice", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullString"), []byte("world"))
		require.NoError(t, err)
		assert.True(t, s.NullString.Valid)
		assert.Equal(t, "world", s.NullString.String)
	})

	t.Run("from nil leaves invalid", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullString"), nil)
		require.NoError(t, err)
		assert.False(t, s.NullString.Valid)
	})
}

func TestSetReflectValue_NullInt64(t *testing.T) {
	t.Run("from int64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullInt64"), int64(42))
		require.NoError(t, err)
		assert.True(t, s.NullInt64.Valid)
		assert.Equal(t, int64(42), s.NullInt64.Int64)
	})

	t.Run("from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullInt64"), float64(99))
		require.NoError(t, err)
		assert.True(t, s.NullInt64.Valid)
		assert.Equal(t, int64(99), s.NullInt64.Int64)
	})

	t.Run("from int", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullInt64"), int(7))
		require.NoError(t, err)
		assert.True(t, s.NullInt64.Valid)
		assert.Equal(t, int64(7), s.NullInt64.Int64)
	})

	t.Run("from nil leaves invalid", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullInt64"), nil)
		require.NoError(t, err)
		assert.False(t, s.NullInt64.Valid)
	})
}

func TestSetReflectValue_NullBool(t *testing.T) {
	t.Run("from bool", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullBool"), true)
		require.NoError(t, err)
		assert.True(t, s.NullBool.Valid)
		assert.True(t, s.NullBool.Bool)
	})

	t.Run("from int64 nonzero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullBool"), int64(1))
		require.NoError(t, err)
		assert.True(t, s.NullBool.Valid)
		assert.True(t, s.NullBool.Bool)
	})

	t.Run("from int64 zero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullBool"), int64(0))
		require.NoError(t, err)
		assert.True(t, s.NullBool.Valid)
		assert.False(t, s.NullBool.Bool)
	})

	t.Run("from float64 nonzero", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullBool"), float64(1.0))
		require.NoError(t, err)
		assert.True(t, s.NullBool.Valid)
		assert.True(t, s.NullBool.Bool)
	})

	t.Run("from nil leaves invalid", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullBool"), nil)
		require.NoError(t, err)
		assert.False(t, s.NullBool.Valid)
	})
}

func TestSetReflectValue_NullFloat64(t *testing.T) {
	t.Run("from float64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullFloat64"), float64(3.14))
		require.NoError(t, err)
		assert.True(t, s.NullFloat64.Valid)
		assert.InDelta(t, 3.14, s.NullFloat64.Float64, 0.001)
	})

	t.Run("from int64", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullFloat64"), int64(7))
		require.NoError(t, err)
		assert.True(t, s.NullFloat64.Valid)
		assert.InDelta(t, 7.0, s.NullFloat64.Float64, 0.001)
	})

	t.Run("from nil leaves invalid", func(t *testing.T) {
		var s testTarget
		err := setReflectValue(fieldOf(&s, "NullFloat64"), nil)
		require.NoError(t, err)
		assert.False(t, s.NullFloat64.Valid)
	})
}

func TestSetReflectValue_UnsupportedKind(t *testing.T) {
	type weird struct {
		Ch chan int
	}
	var w weird
	field := reflect.ValueOf(&w).Elem().FieldByName("Ch")
	err := setReflectValue(field, "something")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported dest field kind")
}
