package utils

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

const (
	snapshotProgressInterval = 30 * time.Second
	snapshotHTTPTimeout      = 30 * time.Second
	snapshotDownloadRetries  = 5
)

type snapshotSource struct {
	rpcURL      string
	archiveURL  string
	archiveRoot string
}

type snapshotMetadata struct {
	URL          string `json:"url"`
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
	Size         int64  `json:"size"`
}

type snapshotRPCResponse struct {
	Result string `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type snapshotSyncer struct {
	client           *http.Client
	logger           *log.Logger
	dataDir          string
	environment      string
	dbEngine         string
	source           snapshotSource
	progressInterval time.Duration
}

func PrepareSnapshotSync(ctx context.Context) error {
	environment := viper.GetString(EnvironmentFlag.Name)
	source, ok := officialSnapshotSource(environment)
	if !ok {
		log.Global.WithField("environment", environment).Warn("Snapshot sync is not available for this environment; continuing with normal sync")
		return nil
	}
	dataDir := filepath.Clean(viper.GetString(DataDirFlag.Name))
	if dataDir == "." || dataDir == string(filepath.Separator) {
		return fmt.Errorf("refusing snapshot sync for unsafe data directory %q", dataDir)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = snapshotHTTPTimeout
	syncer := &snapshotSyncer{
		client:           &http.Client{Transport: transport},
		logger:           log.Global,
		dataDir:          dataDir,
		environment:      environment,
		dbEngine:         viper.GetString(DBEngineFlag.Name),
		source:           source,
		progressInterval: snapshotProgressInterval,
	}
	return syncer.prepare(ctx)
}

func officialSnapshotSource(environment string) (snapshotSource, bool) {
	switch environment {
	case params.ColosseumName:
		return snapshotSource{
			rpcURL:      "https://rpc.quai.network/cyprus1/",
			archiveURL:  "https://snapshot.qu.ai/mainnet-snapshot.tar.zst",
			archiveRoot: "mainnet-snapshot",
		}, true
	case params.OrchardName:
		return snapshotSource{
			rpcURL:      "https://orchard.rpc.quai.network/cyprus1/",
			archiveURL:  "https://snapshot.qu.ai/orchard-snapshot.tar.zst",
			archiveRoot: "orchard-snapshot",
		}, true
	default:
		return snapshotSource{}, false
	}
}

func (syncer *snapshotSyncer) prepare(ctx context.Context) error {
	if err := syncer.recoverInstall(); err != nil {
		return fmt.Errorf("recover interrupted snapshot install: %w", err)
	}
	remoteHeight, err := syncer.rpcHeight(ctx)
	if err != nil {
		return fmt.Errorf("query zone RPC height: %w", err)
	}
	localHeight, localExists, err := syncer.databaseHeight(syncer.dataDir)
	if err != nil {
		return fmt.Errorf("read local zone height: %w", err)
	}
	lag := uint64(0)
	if remoteHeight > localHeight {
		lag = remoteHeight - localHeight
	}
	syncer.logger.WithFields(log.Fields{
		"localHeight":  localHeight,
		"remoteHeight": remoteHeight,
		"lagBlocks":    lag,
		"threshold":    3 * params.BlocksPerWeek,
	}).Info("Snapshot sync height check completed")
	if localExists && lag <= 3*params.BlocksPerWeek {
		syncer.logger.Info("Local zone is within three weeks of the network; snapshot sync is not needed")
		return nil
	}
	if !localExists && remoteHeight <= 3*params.BlocksPerWeek {
		syncer.logger.Info("Network height is below the snapshot threshold; starting normal sync")
		return nil
	}
	workDir := syncer.workDir()
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("create snapshot work directory: %w", err)
	}
	archivePath := filepath.Join(workDir, "snapshot.tar.zst.part")
	metadataPath := filepath.Join(workDir, "snapshot.json")
	metadata, err := syncer.remoteMetadata(ctx)
	if err != nil {
		return fmt.Errorf("inspect remote snapshot: %w", err)
	}
	syncer.logger.WithFields(log.Fields{
		"url":  metadata.URL,
		"size": formatBytes(metadata.Size),
		"etag": metadata.ETag,
	}).Info("Official snapshot selected")
	if err := syncer.downloadWithRetry(ctx, archivePath, metadataPath, metadata); err != nil {
		return err
	}
	stageDir := filepath.Join(workDir, "extracted")
	if err := os.RemoveAll(stageDir); err != nil {
		return fmt.Errorf("clear snapshot staging directory: %w", err)
	}
	if err := syncer.extract(ctx, archivePath, stageDir, metadata.Size); err != nil {
		return fmt.Errorf("extract snapshot: %w", err)
	}
	stageRoot := filepath.Join(stageDir, syncer.source.archiveRoot)
	stagedHeight, exists, err := syncer.databaseHeight(stageRoot)
	if err != nil {
		return fmt.Errorf("validate staged snapshot database: %w", err)
	}
	if !exists {
		return fmt.Errorf("snapshot does not contain a readable zone-0-0 database")
	}
	if stagedHeight <= localHeight {
		staleErr := fmt.Errorf("snapshot height %d is not newer than local height %d", stagedHeight, localHeight)
		cleanupErr := cleanupSnapshotArtifacts(workDir, stageDir, archivePath, metadataPath)
		return errors.Join(staleErr, cleanupErr)
	}
	syncer.logger.WithFields(log.Fields{
		"snapshotHeight": stagedHeight,
		"localHeight":    localHeight,
	}).Info("Snapshot database validation completed")
	if err := syncer.install(stageRoot); err != nil {
		return fmt.Errorf("install snapshot: %w", err)
	}
	if err := cleanupSnapshotArtifacts(workDir, archivePath, metadataPath, stageDir); err != nil {
		syncer.logger.WithField("error", err).Warn("Snapshot installed but temporary files could not be fully removed")
	} else {
		syncer.logger.Info("Snapshot temporary files removed")
	}
	syncer.logger.WithFields(log.Fields{
		"height":  stagedHeight,
		"dataDir": syncer.dataDir,
	}).Info("Snapshot sync completed successfully; starting node")
	return nil
}

func (syncer *snapshotSyncer) rpcHeight(ctx context.Context) (uint64, error) {
	payload := []byte(`{"jsonrpc":"2.0","method":"quai_blockNumber","params":[],"id":1}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, syncer.source.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := syncer.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	var result snapshotRPCResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return 0, err
	}
	if result.Error != nil {
		return 0, fmt.Errorf("RPC error %d: %s", result.Error.Code, result.Error.Message)
	}
	height, err := strconv.ParseUint(strings.TrimPrefix(result.Result, "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid block number %q: %w", result.Result, err)
	}
	return height, nil
}

func (syncer *snapshotSyncer) remoteMetadata(ctx context.Context) (snapshotMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, syncer.source.archiveURL, nil)
	if err != nil {
		return snapshotMetadata{}, err
	}
	resp, err := syncer.client.Do(req)
	if err != nil {
		return snapshotMetadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return snapshotMetadata{}, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	if resp.ContentLength <= 0 {
		return snapshotMetadata{}, errors.New("snapshot server did not provide a content length")
	}
	return snapshotMetadata{
		URL:          syncer.source.archiveURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Size:         resp.ContentLength,
	}, nil
}

func (syncer *snapshotSyncer) downloadWithRetry(ctx context.Context, archivePath, metadataPath string, metadata snapshotMetadata) error {
	var lastErr error
	for attempt := 1; attempt <= snapshotDownloadRetries; attempt++ {
		if err := syncer.download(ctx, archivePath, metadataPath, metadata); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := time.Duration(1<<(attempt-1)) * time.Second
		syncer.logger.WithFields(log.Fields{"attempt": attempt, "retryIn": delay, "error": lastErr}).Warn("Snapshot download interrupted; retrying from saved progress")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("download snapshot after %d attempts: %w", snapshotDownloadRetries, lastErr)
}

func (syncer *snapshotSyncer) download(ctx context.Context, archivePath, metadataPath string, metadata snapshotMetadata) error {
	resumeOffset, err := syncer.resumeOffset(archivePath, metadataPath, metadata)
	if err != nil {
		return err
	}
	if resumeOffset == metadata.Size {
		syncer.logger.WithField("size", formatBytes(metadata.Size)).Info("Snapshot download is already complete")
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.URL, nil)
	if err != nil {
		return err
	}
	if resumeOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeOffset))
		if metadata.ETag != "" {
			req.Header.Set("If-Range", metadata.ETag)
		} else if metadata.LastModified != "" {
			req.Header.Set("If-Range", metadata.LastModified)
		}
	}
	resp, err := syncer.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if resumeOffset > 0 && resp.StatusCode == http.StatusPartialContent {
		if err := validateContentRange(resp.Header.Get("Content-Range"), resumeOffset, metadata.Size); err != nil {
			return err
		}
		flags |= os.O_APPEND
		syncer.logger.WithField("offset", formatBytes(resumeOffset)).Info("Resuming snapshot download")
	} else if resp.StatusCode == http.StatusOK {
		flags |= os.O_TRUNC
		resumeOffset = 0
		syncer.logger.Info("Starting snapshot download")
	} else {
		return fmt.Errorf("unexpected download HTTP status %s", resp.Status)
	}
	file, err := os.OpenFile(archivePath, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	progress := newSnapshotProgress(syncer.logger, "Downloading snapshot", resumeOffset, metadata.Size, syncer.progressInterval)
	_, copyErr := io.CopyBuffer(file, io.TeeReader(resp.Body, progress), make([]byte, 1024*1024))
	progress.finish()
	if copyErr != nil {
		return copyErr
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != metadata.Size {
		return fmt.Errorf("snapshot size mismatch: downloaded %d, expected %d", info.Size(), metadata.Size)
	}
	return nil
}

func (syncer *snapshotSyncer) resumeOffset(archivePath, metadataPath string, metadata snapshotMetadata) (int64, error) {
	storedData, metadataErr := os.ReadFile(metadataPath)
	archiveInfo, archiveErr := os.Stat(archivePath)
	if errors.Is(archiveErr, os.ErrNotExist) {
		archiveInfo = nil
	} else if archiveErr != nil {
		return 0, archiveErr
	}
	var stored snapshotMetadata
	hasRemoteValidator := metadata.ETag != "" || metadata.LastModified != ""
	metadataMatches := hasRemoteValidator && metadataErr == nil && json.Unmarshal(storedData, &stored) == nil && stored == metadata
	if archiveInfo != nil && (!metadataMatches || archiveInfo.Size() > metadata.Size) {
		syncer.logger.Warn("Remote snapshot changed; discarding incompatible partial download")
		if err := os.Remove(archivePath); err != nil {
			return 0, err
		}
		archiveInfo = nil
	}
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(metadataPath, encoded, 0644); err != nil {
		return 0, err
	}
	if archiveInfo == nil {
		return 0, nil
	}
	return archiveInfo.Size(), nil
}

func validateContentRange(value string, expectedStart, expectedSize int64) error {
	if !strings.HasPrefix(value, "bytes ") {
		return fmt.Errorf("invalid Content-Range %q", value)
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid Content-Range %q", value)
	}
	byteRange := strings.Split(parts[0], "-")
	if len(byteRange) != 2 {
		return fmt.Errorf("invalid Content-Range %q", value)
	}
	start, err := strconv.ParseInt(byteRange[0], 10, 64)
	if err != nil || start != expectedStart {
		return fmt.Errorf("unexpected Content-Range start in %q", value)
	}
	end, err := strconv.ParseInt(byteRange[1], 10, 64)
	if err != nil || end != expectedSize-1 {
		return fmt.Errorf("unexpected Content-Range end in %q", value)
	}
	total, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || total != expectedSize {
		return fmt.Errorf("unexpected Content-Range size in %q", value)
	}
	return nil
}

func (syncer *snapshotSyncer) extract(ctx context.Context, archivePath, stageDir string, archiveSize int64) error {
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		return err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	progress := newSnapshotProgress(syncer.logger, "Extracting snapshot", 0, archiveSize, syncer.progressInterval)
	decoder, err := zstd.NewReader(io.TeeReader(file, progress))
	if err != nil {
		return err
	}
	defer decoder.Close()
	tarReader := tar.NewReader(decoder)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		cleanName := filepath.Clean(header.Name)
		if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(stageDir, cleanName)
		relative, err := filepath.Rel(stageDir, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes staging directory: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0644)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyBuffer(output, tarReader, make([]byte, 1024*1024))
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry %q with type %d", header.Name, header.Typeflag)
		}
	}
	progress.finish()
	if _, err := os.Stat(filepath.Join(stageDir, syncer.source.archiveRoot)); err != nil {
		return fmt.Errorf("expected archive root %q not found: %w", syncer.source.archiveRoot, err)
	}
	return nil
}

func (syncer *snapshotSyncer) databaseHeight(root string) (uint64, bool, error) {
	dbDir := filepath.Join(root, "zone-0-0", "go-quai", "chaindata")
	if _, err := os.Stat(dbDir); errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, err
	}
	database, err := rawdb.Open(rawdb.OpenOptions{
		Type:      syncer.dbEngine,
		Directory: dbDir,
		Namespace: "eth/db/chaindata/",
		ReadOnly:  true,
	}, common.ZONE_CTX, syncer.logger, common.Location{0, 0})
	if err != nil {
		return 0, false, err
	}
	defer database.Close()
	headHash := rawdb.ReadHeadBlockHash(database)
	if headHash == (common.Hash{}) {
		return 0, false, nil
	}
	height := rawdb.ReadHeaderNumber(database, headHash)
	if height == nil {
		return 0, false, errors.New("head block number is missing")
	}
	return *height, true, nil
}

func (syncer *snapshotSyncer) install(stageRoot string) error {
	backupDir := syncer.backupDir()
	if err := os.RemoveAll(backupDir); err != nil {
		return err
	}
	activeExists := true
	if _, err := os.Stat(syncer.dataDir); errors.Is(err, os.ErrNotExist) {
		activeExists = false
	} else if err != nil {
		return err
	}
	if activeExists {
		syncer.logger.WithField("backup", backupDir).Info("Moving existing database to snapshot rollback location")
		if err := os.Rename(syncer.dataDir, backupDir); err != nil {
			return err
		}
	}
	if err := os.Rename(stageRoot, syncer.dataDir); err != nil {
		if activeExists {
			if restoreErr := os.Rename(backupDir, syncer.dataDir); restoreErr != nil {
				return fmt.Errorf("activate snapshot: %v; restore previous database: %w", err, restoreErr)
			}
		}
		return err
	}
	if activeExists {
		if err := os.RemoveAll(backupDir); err != nil {
			syncer.logger.WithFields(log.Fields{"backup": backupDir, "error": err}).Warn("Snapshot installed but rollback directory could not be removed")
		} else {
			syncer.logger.WithField("backup", backupDir).Info("Previous database removed after successful snapshot activation")
		}
	}
	return nil
}

func (syncer *snapshotSyncer) recoverInstall() error {
	backupDir := syncer.backupDir()
	stageRoot := filepath.Join(syncer.workDir(), "extracted", syncer.source.archiveRoot)
	_, activeErr := os.Stat(syncer.dataDir)
	_, backupErr := os.Stat(backupDir)
	_, stageErr := os.Stat(stageRoot)
	activeExists := activeErr == nil
	backupExists := backupErr == nil
	stageExists := stageErr == nil
	if err := firstUnexpectedStatError(activeErr, backupErr, stageErr); err != nil {
		return err
	}
	if activeExists && backupExists {
		syncer.logger.Warn("Completing cleanup from an interrupted snapshot installation")
		if err := os.RemoveAll(backupDir); err != nil {
			return err
		}
		syncer.logger.WithField("backup", backupDir).Info("Previous database removed after interrupted snapshot activation")
		return nil
	}
	if !activeExists && backupExists && stageExists {
		syncer.logger.Warn("Completing an interrupted snapshot installation")
		if err := os.Rename(stageRoot, syncer.dataDir); err != nil {
			return err
		}
		if err := os.RemoveAll(backupDir); err != nil {
			return err
		}
		syncer.logger.WithField("backup", backupDir).Info("Previous database removed after completing snapshot activation")
		return nil
	}
	if !activeExists && backupExists {
		syncer.logger.Warn("Restoring database after an interrupted snapshot installation")
		return os.Rename(backupDir, syncer.dataDir)
	}
	return nil
}

func firstUnexpectedStatError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func cleanupSnapshotArtifacts(workDir string, paths ...string) error {
	var cleanupErrors []error
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	for _, directory := range []string{workDir, filepath.Dir(workDir)} {
		if err := removeDirectoryIfEmpty(directory); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove empty directory %s: %w", directory, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func removeDirectoryIfEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return nil
	}
	return os.Remove(path)
}

func (syncer *snapshotSyncer) workDir() string {
	return filepath.Join(syncer.dataDir+".snapshot-sync", syncer.environment)
}

func (syncer *snapshotSyncer) backupDir() string {
	return syncer.dataDir + ".snapshot-backup"
}

type snapshotProgress struct {
	logger       *log.Logger
	phase        string
	written      int64
	total        int64
	started      time.Time
	lastReported time.Time
	interval     time.Duration
}

func newSnapshotProgress(logger *log.Logger, phase string, initial, total int64, interval time.Duration) *snapshotProgress {
	now := time.Now()
	return &snapshotProgress{logger: logger, phase: phase, written: initial, total: total, started: now, lastReported: now, interval: interval}
}

func (progress *snapshotProgress) Write(data []byte) (int, error) {
	progress.written += int64(len(data))
	if time.Since(progress.lastReported) >= progress.interval {
		progress.report()
	}
	return len(data), nil
}

func (progress *snapshotProgress) finish() {
	progress.report()
}

func (progress *snapshotProgress) report() {
	elapsed := time.Since(progress.started)
	rate := float64(progress.written) / elapsed.Seconds()
	fields := log.Fields{
		"downloaded": formatBytes(progress.written),
		"total":      formatBytes(progress.total),
		"elapsed":    elapsed.Round(time.Second),
		"throughput": formatBytes(int64(rate)) + "/s",
	}
	if progress.total > 0 {
		fields["percent"] = fmt.Sprintf("%.2f%%", 100*float64(progress.written)/float64(progress.total))
		if rate > 0 && progress.written < progress.total {
			fields["eta"] = time.Duration(float64(progress.total-progress.written)/rate) * time.Second
		}
	}
	progress.logger.WithFields(fields).Info(progress.phase)
	progress.lastReported = time.Now()
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	divisor, exponent := int64(unit), 0
	for value := size / unit; value >= unit; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(divisor), "KMGTPE"[exponent])
}
