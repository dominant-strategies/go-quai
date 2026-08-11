package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	pebblestore "github.com/dominant-strategies/go-quai/ethdb/pebble"
	"github.com/dominant-strategies/go-quai/log"
)

type snapshotHeadManifest struct {
	Number    uint64 `json:"number"`
	NumberHex string `json:"number_hex"`
	Hash      string `json:"hash"`
}

type snapshotContextManifest struct {
	Path          string               `json:"path"`
	CapturedAt    string               `json:"captured_at"`
	Duration      int64                `json:"duration"`
	DurationHuman string               `json:"duration_human"`
	Ancients      uint64               `json:"ancients"`
	Head          snapshotHeadManifest `json:"head"`
}

type snapshotManifest struct {
	Version   int                                `json:"version"`
	CreatedAt string                             `json:"created_at"`
	DBEngine  string                             `json:"db_engine"`
	Contexts  map[string]snapshotContextManifest `json:"contexts"`
}

type contextSpec struct {
	name     string
	location common.Location
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s SNAPSHOT_DIR\n",
			filepath.Base(os.Args[0]),
		)
		os.Exit(2)
	}

	if err := verifySnapshot(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "SNAPSHOT VERIFY: FAIL: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("SNAPSHOT VERIFY: PASS")
}

func verifySnapshot(snapshotRoot string) error {
	root, err := filepath.Abs(filepath.Clean(snapshotRoot))
	if err != nil {
		return fmt.Errorf("resolve snapshot path: %w", err)
	}

	manifestPath := filepath.Join(root, "manifest.json")

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var manifest snapshotManifest

	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	if manifest.Version != 1 {
		return fmt.Errorf(
			"unsupported manifest version: %d",
			manifest.Version,
		)
	}

	if manifest.DBEngine != "pebble" {
		return fmt.Errorf(
			"unexpected db_engine: %q",
			manifest.DBEngine,
		)
	}

	specs := []contextSpec{
		{
			name:     "prime",
			location: common.Location{},
		},
		{
			name:     "region-0",
			location: common.Location{0},
		},
		{
			name:     "zone-0-0",
			location: common.Location{0, 0},
		},
	}

	fmt.Printf("snapshot=%s\n", root)
	fmt.Printf("created_at=%s\n\n", manifest.CreatedAt)

	for _, spec := range specs {
		entry, ok := manifest.Contexts[spec.name]
		if !ok {
			return fmt.Errorf(
				"%s: missing from manifest",
				spec.name,
			)
		}

		if entry.Ancients != 0 {
			return fmt.Errorf(
				"%s: manifest contains unsupported ancient count %d",
				spec.name,
				entry.Ancients,
			)
		}

		dbPath := filepath.Join(
			root,
			filepath.FromSlash(entry.Path),
		)

		rel, err := filepath.Rel(root, dbPath)
		if err != nil {
			return fmt.Errorf(
				"%s: resolve database path: %w",
				spec.name,
				err,
			)
		}

		if rel == ".." ||
			len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
			return fmt.Errorf(
				"%s: manifest path escapes snapshot root: %q",
				spec.name,
				entry.Path,
			)
		}

		if _, err := os.Stat(
			filepath.Join(dbPath, "CURRENT"),
		); err != nil {
			return fmt.Errorf(
				"%s: missing CURRENT: %w",
				spec.name,
				err,
			)
		}

		db, err := pebblestore.New(
			dbPath,
			16,
			16,
			"snapshot/verify",
			true,
			log.Global,
			spec.location,
		)
		if err != nil {
			return fmt.Errorf(
				"%s: open Pebble read-only: %w",
				spec.name,
				err,
			)
		}

		headHash := rawdb.ReadHeadBlockHash(db)

		if headHash == (common.Hash{}) {
			db.Close()

			return fmt.Errorf(
				"%s: no canonical head hash",
				spec.name,
			)
		}

		headNumber := rawdb.ReadHeaderNumber(
			db,
			headHash,
		)

		if headNumber == nil {
			db.Close()

			return fmt.Errorf(
				"%s: no number for head hash %s",
				spec.name,
				headHash.String(),
			)
		}

		if err := db.Close(); err != nil {
			return fmt.Errorf(
				"%s: close Pebble: %w",
				spec.name,
				err,
			)
		}

		actualHash := headHash.String()
		actualHex := fmt.Sprintf(
			"0x%x",
			*headNumber,
		)

		fmt.Printf(
			"%-10s number=%d hex=%s hash=%s\n",
			spec.name,
			*headNumber,
			actualHex,
			actualHash,
		)

		if *headNumber != entry.Head.Number {
			return fmt.Errorf(
				"%s: head number mismatch: manifest=%d actual=%d",
				spec.name,
				entry.Head.Number,
				*headNumber,
			)
		}

		if actualHex != entry.Head.NumberHex {
			return fmt.Errorf(
				"%s: head hex mismatch: manifest=%s actual=%s",
				spec.name,
				entry.Head.NumberHex,
				actualHex,
			)
		}

		if actualHash != entry.Head.Hash {
			return fmt.Errorf(
				"%s: head hash mismatch: manifest=%s actual=%s",
				spec.name,
				entry.Head.Hash,
				actualHash,
			)
		}

		fmt.Printf("%-10s PASS\n\n", spec.name)
	}

	return nil
}
