// Package database provides type scanners that handle differences between
// SQLite and PostgreSQL value representations transparently.
package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Common time formats used by SQLite (stored as TEXT).
var sqliteTimeFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Time is a time.Time that scans from both SQLite TEXT and PostgreSQL TIMESTAMPTZ.
type Time struct {
	time.Time
}

func (t *Time) Scan(src any) error {
	if src == nil {
		t.Time = time.Time{}
		return nil
	}
	switch v := src.(type) {
	case time.Time: // PostgreSQL returns time.Time
		t.Time = v
	case string: // SQLite returns TEXT
		parsed, err := parseSQLiteTime(v)
		if err != nil {
			return fmt.Errorf("scan Time from string %q: %w", v, err)
		}
		t.Time = parsed
	case []byte: // Some drivers return []byte
		parsed, err := parseSQLiteTime(string(v))
		if err != nil {
			return fmt.Errorf("scan Time from bytes: %w", err)
		}
		t.Time = parsed
	default:
		return fmt.Errorf("scan Time: unsupported type %T", src)
	}
	return nil
}

func (t Time) Value() (driver.Value, error) {
	if t.Time.IsZero() {
		return nil, nil
	}
	return t.Time, nil
}

// NullTime is like Time but allows NULL values.
type NullTime struct {
	Time  time.Time
	Valid bool
}

func (nt *NullTime) Scan(src any) error {
	if src == nil {
		nt.Valid = false
		nt.Time = time.Time{}
		return nil
	}
	nt.Valid = true
	t := &Time{}
	if err := t.Scan(src); err != nil {
		return err
	}
	nt.Time = t.Time
	return nil
}

func (nt NullTime) Value() (driver.Value, error) {
	if !nt.Valid {
		return nil, nil
	}
	return nt.Time, nil
}

// Bool scans from both SQLite INTEGER (0/1) and PostgreSQL BOOLEAN.
type Bool bool

func (b *Bool) Scan(src any) error {
	if src == nil {
		*b = false
		return nil
	}
	switch v := src.(type) {
	case bool: // PostgreSQL
		*b = Bool(v)
	case int64: // SQLite
		*b = v != 0
	case float64: // Some drivers
		*b = v != 0
	default:
		return fmt.Errorf("scan Bool: unsupported type %T", src)
	}
	return nil
}

func (b Bool) Value() (driver.Value, error) {
	return bool(b), nil
}

// NullBool is like Bool but allows NULL values.
type NullBool struct {
	Bool  bool
	Valid bool
}

func (nb *NullBool) Scan(src any) error {
	if src == nil {
		nb.Valid = false
		nb.Bool = false
		return nil
	}
	nb.Valid = true
	b := Bool(false)
	if err := b.Scan(src); err != nil {
		return err
	}
	nb.Bool = bool(b)
	return nil
}

func (nb NullBool) Value() (driver.Value, error) {
	if !nb.Valid {
		return nil, nil
	}
	return nb.Bool, nil
}

// JSON is a scanner/valuer for JSON columns.
// SQLite stores JSON as TEXT; PostgreSQL stores as JSONB.
// T is the Go type to unmarshal into.
type JSON[T any] struct {
	Val   T
	Valid bool
}

func (j *JSON[T]) Scan(src any) error {
	if src == nil {
		j.Valid = false
		return nil
	}
	j.Valid = true
	var data []byte
	switch v := src.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("scan JSON: unsupported type %T", src)
	}
	return json.Unmarshal(data, &j.Val)
}

func (j JSON[T]) Value() (driver.Value, error) {
	if !j.Valid {
		return nil, nil
	}
	data, err := json.Marshal(j.Val)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// NullString is a convenience alias for sql.NullString.
// Included here for API consistency.
type NullString struct {
	String string
	Valid  bool
}

func (ns *NullString) Scan(src any) error {
	if src == nil {
		ns.Valid = false
		ns.String = ""
		return nil
	}
	ns.Valid = true
	switch v := src.(type) {
	case string:
		ns.String = v
	case []byte:
		ns.String = string(v)
	default:
		return fmt.Errorf("scan NullString: unsupported type %T", src)
	}
	return nil
}

func (ns NullString) Value() (driver.Value, error) {
	if !ns.Valid {
		return nil, nil
	}
	return ns.String, nil
}

// NullInt64 scans nullable integers from both drivers.
type NullInt64 struct {
	Int64 int64
	Valid bool
}

func (ni *NullInt64) Scan(src any) error {
	if src == nil {
		ni.Valid = false
		ni.Int64 = 0
		return nil
	}
	ni.Valid = true
	switch v := src.(type) {
	case int64:
		ni.Int64 = v
	case float64:
		ni.Int64 = int64(v)
	default:
		return fmt.Errorf("scan NullInt64: unsupported type %T", src)
	}
	return nil
}

func (ni NullInt64) Value() (driver.Value, error) {
	if !ni.Valid {
		return nil, nil
	}
	return ni.Int64, nil
}

// NullFloat64 scans nullable floats from both drivers.
type NullFloat64 struct {
	Float64 float64
	Valid   bool
}

func (nf *NullFloat64) Scan(src any) error {
	if src == nil {
		nf.Valid = false
		nf.Float64 = 0
		return nil
	}
	nf.Valid = true
	switch v := src.(type) {
	case float64:
		nf.Float64 = v
	case int64:
		nf.Float64 = float64(v)
	default:
		return fmt.Errorf("scan NullFloat64: unsupported type %T", src)
	}
	return nil
}

func (nf NullFloat64) Value() (driver.Value, error) {
	if !nf.Valid {
		return nil, nil
	}
	return nf.Float64, nil
}

// parseSQLiteTime tries multiple formats to parse a SQLite time string.
func parseSQLiteTime(s string) (time.Time, error) {
	for _, format := range sqliteTimeFormats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

// FormatTime returns a time formatted for insertion.
// SQLite needs RFC3339 string; PG driver accepts time.Time directly.
// Use this when building raw SQL with string interpolation (rare).
func FormatTime(t time.Time, dialect DialectKind) string {
	switch dialect {
	case DialectPostgres:
		return t.Format(time.RFC3339Nano)
	default: // SQLite
		return t.Format(time.RFC3339)
	}
}

// FormatNow returns the current time formatted for the dialect.
func FormatNow(dialect DialectKind) string {
	return FormatTime(time.Now().UTC(), dialect)
}
