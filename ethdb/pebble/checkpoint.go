// Copyright 2026
//
// Experimental online checkpoint support for go-quai Pebble databases.

package pebble

import "github.com/cockroachdb/pebble"

// Checkpoint creates a standalone point-in-time copy of the open Pebble
// database while allowing the live database to continue serving reads/writes.
func (d *Database) Checkpoint(destDir string) error {
	d.quitLock.RLock()
	defer d.quitLock.RUnlock()

	if d.closed {
		return pebble.ErrClosed
	}

	return d.db.Checkpoint(destDir)
}
