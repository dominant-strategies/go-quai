// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package node

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/dominant-strategies/go-quai/log"
)

// Tests that datadirs can be successfully created, be them manually configured
// ones or automatically generated temporary ones.
func TestDatadirCreation(t *testing.T) {
	// Create a temporary data dir and check that it can be used by a node
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("failed to create manual data dir: %v", err)
	}
	defer os.RemoveAll(dir)

	node, err := New(&Config{DataDir: dir}, log.Global)
	if err != nil {
		t.Fatalf("failed to create stack with existing datadir: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("failed to close node: %v", err)
	}
	// Generate a long non-existing datadir path and check that it gets created by a node
	dir = filepath.Join(dir, "a", "b", "c", "d", "e", "f")
	node, err = New(&Config{DataDir: dir}, log.Global)
	if err != nil {
		t.Fatalf("failed to create stack with creatable datadir: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("failed to close node: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("freshly created datadir not accessible: %v", err)
	}
	// Verify that an impossible datadir fails creation
	file, err := ioutil.TempFile("", "")
	if err != nil {
		t.Fatalf("failed to create temporary file: %v", err)
	}
	defer os.Remove(file.Name())

	dir = filepath.Join(file.Name(), "invalid/path")
	node, err = New(&Config{DataDir: dir}, log.Global)
	if err == nil {
		t.Fatalf("protocol stack created with an invalid datadir")
		if err := node.Close(); err != nil {
			t.Fatalf("failed to close node: %v", err)
		}
	}
}
