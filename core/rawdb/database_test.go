package rawdb

import "testing"

func TestResolveDbType(t *testing.T) {
	t.Run("prefers existing database type", func(t *testing.T) {
		dbType, err := resolveDbType("", dbLeveldb)
		if err != nil {
			t.Fatalf("resolveDbType returned unexpected error: %v", err)
		}
		if dbType != dbLeveldb {
			t.Fatalf("resolveDbType returned %q, want %q", dbType, dbLeveldb)
		}
	})

	t.Run("rejects mismatched explicit type", func(t *testing.T) {
		if _, err := resolveDbType(dbPebble, dbLeveldb); err == nil {
			t.Fatal("resolveDbType accepted an explicit type that mismatched the existing database")
		}
	})

	t.Run("defaults to the fastest supported engine for new databases", func(t *testing.T) {
		dbType, err := resolveDbType("", "")
		if err != nil {
			t.Fatalf("resolveDbType returned unexpected error: %v", err)
		}
		expected := dbLeveldb
		if PebbleEnabled {
			expected = dbPebble
		}
		if dbType != expected {
			t.Fatalf("resolveDbType returned %q, want %q", dbType, expected)
		}
	})

	t.Run("honors explicit engine choice", func(t *testing.T) {
		dbType, err := resolveDbType(dbPebble, "")
		if err != nil {
			t.Fatalf("resolveDbType returned unexpected error: %v", err)
		}
		if dbType != dbPebble {
			t.Fatalf("resolveDbType returned %q, want %q", dbType, dbPebble)
		}
	})
}
