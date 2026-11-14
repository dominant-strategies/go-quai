package rawdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/ethdb"
	pebblestore "github.com/dominant-strategies/go-quai/ethdb/pebble"
	"github.com/dominant-strategies/go-quai/log"
)

type snapshotRegistration struct {
	name     string
	db       ethdb.Database
	location common.Location
}

type SnapshotHeadManifest struct {
	Number    uint64 `json:"number"`
	NumberHex string `json:"number_hex"`
	Hash      string `json:"hash"`
}

type SnapshotContextManifest struct {
	Path          string               `json:"path"`
	CapturedAt    time.Time            `json:"captured_at"`
	Duration      time.Duration        `json:"duration"`
	DurationHuman string               `json:"duration_human"`
	Ancients      uint64               `json:"ancients"`
	Head          SnapshotHeadManifest `json:"head"`
}

type SnapshotManifest struct {
	Version   int                                `json:"version"`
	CreatedAt time.Time                          `json:"created_at"`
	DBEngine  string                             `json:"db_engine"`
	Contexts  map[string]SnapshotContextManifest `json:"contexts"`
}

var pebbleSnapshotCoordinator = struct {
	sync.Mutex
	root      string
	dbs       map[string]snapshotRegistration
	capturing bool
}{
	dbs: make(map[string]snapshotRegistration),
}

func unwrapPebble(db interface{}) (*pebblestore.Database, error) {
	switch db := db.(type) {
	case interface{ UnwrapDatabase() ethdb.Database }:
		return unwrapPebble(db.UnwrapDatabase())

	case *freezerdb:
		return unwrapPebble(db.KeyValueStore)

	case *nofreezedb:
		return unwrapPebble(db.KeyValueStore)

	case *pebblestore.Database:
		return db, nil

	default:
		return nil, fmt.Errorf(
			"snapshot requires Pebble backing database, got %T",
			db,
		)
	}
}

// RegisterPebbleSnapshotDatabase registers one live chain context with the
// process-wide snapshot coordinator. Registration does not itself trigger a
// snapshot.
func RegisterPebbleSnapshotDatabase(
	root string,
	name string,
	db ethdb.Database,
	location common.Location,
	logger *log.Logger,
) {
	if root == "" {
		return
	}

	switch name {
	case "prime", "region-0", "zone-0-0":
	default:
		logger.WithField("context", name).Error(
			"Refusing unknown context in coordinated Pebble snapshot",
		)
		return
	}

	pebbleSnapshotCoordinator.Lock()
	defer pebbleSnapshotCoordinator.Unlock()

	if pebbleSnapshotCoordinator.root == "" {
		pebbleSnapshotCoordinator.root = root
	} else if pebbleSnapshotCoordinator.root != root {
		logger.WithFields(log.Fields{
			"existingRoot": pebbleSnapshotCoordinator.root,
			"newRoot":      root,
		}).Error("Snapshot root changed while registering databases")
		return
	}

	pebbleSnapshotCoordinator.dbs[name] = snapshotRegistration{
		name:     name,
		db:       db,
		location: location,
	}

	logger.WithFields(log.Fields{
		"context":    name,
		"registered": len(pebbleSnapshotCoordinator.dbs),
		"required":   3,
		"root":       root,
	}).Info("Registered database for coordinated Pebble snapshot")
}

// TriggerPebbleSnapshot starts one asynchronous coordinated snapshot.
//
// It returns an error immediately if registration is incomplete or another
// snapshot is already running.
func TriggerPebbleSnapshot(logger *log.Logger) error {
	pebbleSnapshotCoordinator.Lock()

	if pebbleSnapshotCoordinator.capturing {
		pebbleSnapshotCoordinator.Unlock()
		return fmt.Errorf("Pebble snapshot already in progress")
	}

	required := []string{"prime", "region-0", "zone-0-0"}

	if pebbleSnapshotCoordinator.root == "" {
		pebbleSnapshotCoordinator.Unlock()
		return fmt.Errorf("Pebble snapshot root is not configured")
	}

	for _, name := range required {
		if _, ok := pebbleSnapshotCoordinator.dbs[name]; !ok {
			pebbleSnapshotCoordinator.Unlock()
			return fmt.Errorf(
				"Pebble snapshot database %q is not registered",
				name,
			)
		}
	}

	root := pebbleSnapshotCoordinator.root

	dbs := []snapshotRegistration{
		pebbleSnapshotCoordinator.dbs["prime"],
		pebbleSnapshotCoordinator.dbs["region-0"],
		pebbleSnapshotCoordinator.dbs["zone-0-0"],
	}

	pebbleSnapshotCoordinator.capturing = true
	pebbleSnapshotCoordinator.Unlock()

	go func() {
		start := time.Now()

		err := capturePebbleSnapshotSet(root, dbs, logger)

		pebbleSnapshotCoordinator.Lock()
		pebbleSnapshotCoordinator.capturing = false
		pebbleSnapshotCoordinator.Unlock()

		if err != nil {
			logger.WithFields(log.Fields{
				"err":      err,
				"duration": time.Since(start),
			}).Error("Coordinated Pebble snapshot failed")
			return
		}

		logger.WithField(
			"duration",
			time.Since(start),
		).Warn("Snapshot request completed")
	}()

	return nil
}

func readCheckpointHead(
	dest string,
	location common.Location,
	logger *log.Logger,
) (SnapshotHeadManifest, error) {
	db, err := pebblestore.New(
		dest,
		16,
		16,
		"snapshot/verify",
		true,
		logger,
		location,
	)
	if err != nil {
		return SnapshotHeadManifest{}, fmt.Errorf(
			"open checkpoint read-only: %w",
			err,
		)
	}
	defer db.Close()

	headHash := ReadHeadBlockHash(db)
	if headHash == (common.Hash{}) {
		return SnapshotHeadManifest{}, fmt.Errorf(
			"checkpoint has no canonical head block hash",
		)
	}

	headNumber := ReadHeaderNumber(db, headHash)
	if headNumber == nil {
		return SnapshotHeadManifest{}, fmt.Errorf(
			"checkpoint head hash %s has no block number",
			headHash.String(),
		)
	}

	return SnapshotHeadManifest{
		Number:    *headNumber,
		NumberHex: fmt.Sprintf("0x%x", *headNumber),
		Hash:      headHash.String(),
	}, nil
}

func capturePebbleSnapshotSet(
	root string,
	dbs []snapshotRegistration,
	logger *log.Logger,
) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create snapshot root: %w", err)
	}

	now := time.Now().UTC()
	stamp := now.Format("20060102T150405.000000000Z")

	building := filepath.Join(
		root,
		fmt.Sprintf(".building-%s-%d", stamp, os.Getpid()),
	)
	final := filepath.Join(root, "quai-snapshot-"+stamp)

	if _, err := os.Stat(building); !os.IsNotExist(err) {
		return fmt.Errorf(
			"building destination already exists: %s",
			building,
		)
	}

	if _, err := os.Stat(final); !os.IsNotExist(err) {
		return fmt.Errorf(
			"final destination already exists: %s",
			final,
		)
	}

	if err := os.Mkdir(building, 0755); err != nil {
		return fmt.Errorf("create building directory: %w", err)
	}

	success := false

	defer func() {
		if !success {
			if err := os.RemoveAll(building); err != nil {
				logger.WithFields(log.Fields{
					"path": building,
					"err":  err,
				}).Error("Failed cleaning incomplete snapshot")
			}
		}
	}()

	// V1 checkpoints only the Pebble KV database. Until freezer files are
	// included in the snapshot format, refuse to produce an incomplete set
	// whenever any context contains ancient records.
	for _, entry := range dbs {
		ancients, err := entry.db.Ancients()
		if err != nil {
			return fmt.Errorf(
				"%s: cannot determine ancient count: %w",
				entry.name,
				err,
			)
		}

		if ancients != 0 {
			return fmt.Errorf(
				"%s: refusing incomplete snapshot: ancient count is %d",
				entry.name,
				ancients,
			)
		}
	}

	manifest := SnapshotManifest{
		Version:   1,
		CreatedAt: now,
		DBEngine:  "pebble",
		Contexts:  make(map[string]SnapshotContextManifest, len(dbs)),
	}

	for _, entry := range dbs {
		pebbleDB, err := unwrapPebble(entry.db)
		if err != nil {
			return fmt.Errorf("%s: %w", entry.name, err)
		}

		rel := filepath.Join(
			entry.name,
			"go-quai",
			"chaindata",
		)
		dest := filepath.Join(building, rel)

		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf(
				"%s: create destination parent: %w",
				entry.name,
				err,
			)
		}

		start := time.Now()

		logger.WithFields(log.Fields{
			"context":     entry.name,
			"destination": dest,
		}).Warn("Starting native Pebble checkpoint")

		if err := pebbleDB.Checkpoint(dest); err != nil {
			return fmt.Errorf(
				"%s checkpoint: %w",
				entry.name,
				err,
			)
		}

		duration := time.Since(start)
		capturedAt := time.Now().UTC()

		ancients, err := entry.db.Ancients()
		if err != nil {
			return fmt.Errorf(
				"%s: cannot re-check ancient count: %w",
				entry.name,
				err,
			)
		}

		if ancients != 0 {
			return fmt.Errorf(
				"%s: ancient count changed during capture to %d",
				entry.name,
				ancients,
			)
		}

		if err := os.MkdirAll(
			filepath.Join(dest, "ancient"),
			0755,
		); err != nil {
			return fmt.Errorf(
				"%s: create empty ancient directory: %w",
				entry.name,
				err,
			)
		}

		head, err := readCheckpointHead(
			dest,
			entry.location,
			logger,
		)
		if err != nil {
			return fmt.Errorf(
				"%s: read frozen checkpoint head: %w",
				entry.name,
				err,
			)
		}

		manifest.Contexts[entry.name] = SnapshotContextManifest{
			Path:          rel,
			CapturedAt:    capturedAt,
			Duration:      duration,
			DurationHuman: duration.String(),
			Ancients:      ancients,
			Head:          head,
		}

		logger.WithFields(log.Fields{
			"context":  entry.name,
			"duration": duration,
		}).Warn("Native Pebble checkpoint completed")
	}

	// Re-check the entire set immediately before publication. A freezer could
	// theoretically advance in an earlier context while a later context was
	// being checkpointed.
	for _, entry := range dbs {
		ancients, err := entry.db.Ancients()
		if err != nil {
			return fmt.Errorf(
				"%s: cannot perform final ancient-count check: %w",
				entry.name,
				err,
			)
		}
		if ancients != 0 {
			return fmt.Errorf(
				"%s: refusing publication: ancient count changed to %d",
				entry.name,
				ancients,
			)
		}
	}

	manifestPath := filepath.Join(building, "manifest.json")

	f, err := os.OpenFile(
		manifestPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0644,
	)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(&manifest); err != nil {
		f.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}

	if err := os.Rename(building, final); err != nil {
		return fmt.Errorf(
			"publish completed snapshot directory: %w",
			err,
		)
	}

	success = true

	logger.WithFields(log.Fields{
		"path":      final,
		"createdAt": manifest.CreatedAt,
		"contexts":  len(manifest.Contexts),
		"dbEngine":  manifest.DBEngine,
	}).Warn("Coordinated Pebble snapshot completed")

	return nil
}
