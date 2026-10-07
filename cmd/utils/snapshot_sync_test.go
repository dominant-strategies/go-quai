package utils

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestOfficialSnapshotSource(t *testing.T) {
	mainnet, ok := officialSnapshotSource(params.ColosseumName)
	require.True(t, ok)
	assert.Equal(t, "https://rpc.quai.network/cyprus1/", mainnet.rpcURL)
	assert.Equal(t, "mainnet-snapshot", mainnet.archiveRoot)

	orchard, ok := officialSnapshotSource(params.OrchardName)
	require.True(t, ok)
	assert.Equal(t, "https://orchard.rpc.quai.network/cyprus1/", orchard.rpcURL)
	assert.Equal(t, "orchard-snapshot", orchard.archiveRoot)

	_, ok = officialSnapshotSource(params.LocalName)
	assert.False(t, ok)
}

func TestSnapshotRPCHeight(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, request.Method)
		return httpResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`, nil), nil
	})}

	syncer := testSnapshotSyncer(t)
	syncer.client = client
	syncer.source.rpcURL = "https://rpc.example.test"
	height, err := syncer.rpcHeight(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(42), height)
}

func TestSnapshotDownloadResumes(t *testing.T) {
	content := []byte("complete snapshot content")
	metadata := snapshotMetadata{URL: "unused", ETag: `"snapshot-v1"`, Size: int64(len(content))}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "bytes=8-", request.Header.Get("Range"))
		assert.Equal(t, metadata.ETag, request.Header.Get("If-Range"))
		headers := http.Header{"Content-Range": []string{"bytes 8-24/25"}}
		return httpResponse(http.StatusPartialContent, string(content[8:]), headers), nil
	})}
	metadata.URL = "https://snapshot.example.test/snapshot.tar.zst"

	syncer := testSnapshotSyncer(t)
	syncer.client = client
	archivePath := filepath.Join(t.TempDir(), "snapshot.part")
	metadataPath := filepath.Join(filepath.Dir(archivePath), "snapshot.json")
	require.NoError(t, os.WriteFile(archivePath, content[:8], 0644))
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadataPath, encoded, 0644))

	require.NoError(t, syncer.download(context.Background(), archivePath, metadataPath, metadata))
	downloaded, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded)
}

func TestSnapshotResumeDiscardsChangedArchive(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "snapshot.part")
	metadataPath := filepath.Join(directory, "snapshot.json")
	require.NoError(t, os.WriteFile(archivePath, []byte("partial"), 0644))
	oldMetadata := snapshotMetadata{URL: "https://example.invalid/old", ETag: `"old"`, Size: 100}
	encoded, err := json.Marshal(oldMetadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadataPath, encoded, 0644))

	newMetadata := snapshotMetadata{URL: "https://example.invalid/new", ETag: `"new"`, Size: 200}
	syncer := testSnapshotSyncer(t)
	offset, err := syncer.resumeOffset(archivePath, metadataPath, newMetadata)
	require.NoError(t, err)
	assert.Zero(t, offset)
	_, err = os.Stat(archivePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSnapshotResumeRequiresRemoteValidator(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "snapshot.part")
	metadataPath := filepath.Join(directory, "snapshot.json")
	metadata := snapshotMetadata{URL: "https://example.invalid/snapshot", Size: 100}
	require.NoError(t, os.WriteFile(archivePath, []byte("partial"), 0644))
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadataPath, encoded, 0644))

	syncer := testSnapshotSyncer(t)
	offset, err := syncer.resumeOffset(archivePath, metadataPath, metadata)
	require.NoError(t, err)
	assert.Zero(t, offset)
	_, err = os.Stat(archivePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestValidateContentRange(t *testing.T) {
	require.NoError(t, validateContentRange("bytes 8-24/25", 8, 25))
	require.Error(t, validateContentRange("bytes 7-24/25", 8, 25))
	require.Error(t, validateContentRange("bytes 8-23/25", 8, 25))
	require.Error(t, validateContentRange("bytes 8-24/26", 8, 25))
}

func TestSnapshotExtractRejectsUnsafePaths(t *testing.T) {
	syncer := testSnapshotSyncer(t)
	syncer.source.archiveRoot = "mainnet-snapshot"
	archivePath := filepath.Join(t.TempDir(), "snapshot.tar.zst")
	writeSnapshotArchive(t, archivePath, map[string]string{"../escape": "bad"})

	err := syncer.extract(context.Background(), archivePath, t.TempDir(), fileSize(t, archivePath))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe archive path")
}

func TestSnapshotExtractsRegularFiles(t *testing.T) {
	syncer := testSnapshotSyncer(t)
	syncer.source.archiveRoot = "mainnet-snapshot"
	archivePath := filepath.Join(t.TempDir(), "snapshot.tar.zst")
	writeSnapshotArchive(t, archivePath, map[string]string{"mainnet-snapshot/zone-0-0/go-quai/chaindata/CURRENT": "manifest"})
	stageDir := t.TempDir()

	require.NoError(t, syncer.extract(context.Background(), archivePath, stageDir, fileSize(t, archivePath)))
	contents, err := os.ReadFile(filepath.Join(stageDir, "mainnet-snapshot", "zone-0-0", "go-quai", "chaindata", "CURRENT"))
	require.NoError(t, err)
	assert.Equal(t, "manifest", string(contents))
}

func TestSnapshotInstallReplacesDataAtomically(t *testing.T) {
	root := t.TempDir()
	syncer := testSnapshotSyncer(t)
	syncer.dataDir = filepath.Join(root, "go-quai")
	require.NoError(t, os.MkdirAll(syncer.dataDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(syncer.dataDir, "old"), []byte("old"), 0644))
	stageRoot := filepath.Join(root, "stage")
	require.NoError(t, os.MkdirAll(stageRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stageRoot, "new"), []byte("new"), 0644))

	require.NoError(t, syncer.install(stageRoot))
	_, err := os.Stat(filepath.Join(syncer.dataDir, "new"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(syncer.dataDir, "old"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(syncer.backupDir())
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSnapshotRecoveryCompletesInterruptedInstall(t *testing.T) {
	root := t.TempDir()
	syncer := testSnapshotSyncer(t)
	syncer.dataDir = filepath.Join(root, "go-quai")
	syncer.environment = params.ColosseumName
	syncer.source.archiveRoot = "mainnet-snapshot"
	require.NoError(t, os.MkdirAll(syncer.backupDir(), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(syncer.backupDir(), "old"), []byte("old"), 0644))
	stageRoot := filepath.Join(syncer.workDir(), "extracted", syncer.source.archiveRoot)
	require.NoError(t, os.MkdirAll(stageRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stageRoot, "new"), []byte("new"), 0644))

	require.NoError(t, syncer.recoverInstall())
	_, err := os.Stat(filepath.Join(syncer.dataDir, "new"))
	require.NoError(t, err)
	_, err = os.Stat(syncer.backupDir())
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSnapshotArtifactCleanupRemovesEmptyWorkDirectories(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "go-quai.snapshot-sync", params.ColosseumName)
	stageDir := filepath.Join(workDir, "extracted")
	archivePath := filepath.Join(workDir, "snapshot.tar.zst.part")
	metadataPath := filepath.Join(workDir, "snapshot.json")
	require.NoError(t, os.MkdirAll(stageDir, 0755))
	require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0644))
	require.NoError(t, os.WriteFile(metadataPath, []byte("metadata"), 0644))

	require.NoError(t, cleanupSnapshotArtifacts(workDir, archivePath, metadataPath, stageDir))
	_, err := os.Stat(workDir)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Dir(workDir))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSnapshotArtifactCleanupPreservesNonemptyParent(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "go-quai.snapshot-sync")
	workDir := filepath.Join(parentDir, params.ColosseumName)
	otherEnvironment := filepath.Join(parentDir, params.OrchardName)
	require.NoError(t, os.MkdirAll(workDir, 0755))
	require.NoError(t, os.MkdirAll(otherEnvironment, 0755))

	require.NoError(t, cleanupSnapshotArtifacts(workDir))
	_, err := os.Stat(workDir)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(otherEnvironment)
	require.NoError(t, err)
}

func testSnapshotSyncer(t *testing.T) *snapshotSyncer {
	t.Helper()
	logger := log.NewLogger(filepath.Join(t.TempDir(), "snapshot-test.log"), "error", 1)
	logger.SetOutput(io.Discard)
	return &snapshotSyncer{
		client:           http.DefaultClient,
		logger:           logger,
		dataDir:          filepath.Join(t.TempDir(), "go-quai"),
		environment:      params.ColosseumName,
		dbEngine:         "leveldb",
		progressInterval: time.Hour,
	}
}

func writeSnapshotArchive(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	encoder, err := zstd.NewWriter(file)
	require.NoError(t, err)
	tarWriter := tar.NewWriter(encoder)
	for name, contents := range files {
		header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		require.NoError(t, tarWriter.WriteHeader(header))
		_, err := io.Copy(tarWriter, bytes.NewBufferString(contents))
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, encoder.Close())
	require.NoError(t, file.Close())
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func httpResponse(status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     headers,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
