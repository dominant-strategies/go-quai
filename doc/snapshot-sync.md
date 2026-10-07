# Snapshot Startup Sync

## Client behavior

Enable automatic snapshot startup sync with:

```shell
go-quai start --node.snapshot-sync
```

The flag defaults to `false`. It currently supports the official Colosseum Mainnet and Orchard Testnet snapshots. Other environments log a warning and continue with normal peer syncing.

Before creating P2P or database services, go-quai:

1. Queries `quai_blockNumber` from the official Cyprus-1 RPC.
2. Reads the local `zone-0-0` database height.
3. Continues normal startup when the local database is no more than `3 * params.BlocksPerWeek` behind.
4. Downloads the official environment snapshot when the lag exceeds that threshold.
5. Extracts into a staging directory and rejects absolute paths, path traversal, links, and unsupported archive entries.
6. Verifies that the staged snapshot contains a readable zone database newer than the current local database.
7. Replaces the data directory using same-filesystem renames, retaining the previous directory until activation succeeds.

The official snapshot is trusted input. Operators who require independent verification must sync from genesis.

## Resume and recovery

Temporary state is stored outside the active data directory:

```text
<data-dir>.snapshot-sync/<environment>/snapshot.tar.zst.part
<data-dir>.snapshot-sync/<environment>/snapshot.json
<data-dir>.snapshot-sync/<environment>/extracted/
<data-dir>.snapshot-backup
```

The downloader records the archive URL, size, `ETag`, and `Last-Modified` value. A later startup resumes with `Range` and `If-Range` only when the remote validator still matches. If the server publishes a new archive, the incompatible partial file is discarded.

`SIGINT` and `SIGTERM` stop download or extraction before node services start. Partial downloads remain resumable. Extraction restarts from the beginning because zstd streams cannot be resumed safely at an arbitrary compressed offset.

Installation is recoverable across process or machine failure:

- If activation never began, the existing data directory remains untouched.
- If the old directory was moved but the staged snapshot was not activated, the next startup completes activation when staging exists.
- If staging is unavailable, the next startup restores the backup.
- If activation completed but cleanup did not, the next startup removes the stale backup.

After successful activation, go-quai removes the downloaded archive, metadata, extraction staging directory, and empty snapshot work directories before starting node services. It also removes the previous database from `<data-dir>.snapshot-backup`. Cleanup failures are logged with the affected path; they do not prevent the newly activated database from starting.

Snapshot archives are large. Available disk must accommodate the compressed archive, extracted snapshot, and existing database until the final rename and cleanup complete.

## Logs

The global log reports:

- Local height, remote height, lag, and the three-week threshold.
- Snapshot URL, compressed size, `ETag`, and resume offset.
- Download and extraction percentage, throughput, elapsed time, and ETA every 30 seconds.
- Retry delay and error for interrupted transfers.
- Staged database height and local database height.
- Backup, activation, rollback, recovery, and cleanup actions.

If RPC lookup, download, extraction, or validation fails while the existing database is intact, go-quai logs the error and continues normal syncing. An operator interrupt exits before starting the node and preserves download progress.

## Current Nginx requirements

The current client works with the existing static snapshot URLs and does not require a server deployment. Nginx must provide:

- Successful `HEAD` responses with `Content-Length`.
- A stable `ETag` or `Last-Modified` validator for the published file.
- Byte-range `GET` responses using status `206` and a correct `Content-Range` header.
- The unmodified `.tar.zst` bytes without dynamic compression or content transformation.

A minimal static-file configuration is:

```nginx
location = /mainnet-snapshot.tar.zst {
    root /srv/quai-snapshots;
    sendfile on;
    aio threads;
    etag on;
    gzip off;
    add_header Cache-Control "public, max-age=3600" always;
}

location = /orchard-snapshot.tar.zst {
    root /srv/quai-snapshots;
    sendfile on;
    aio threads;
    etag on;
    gzip off;
    add_header Cache-Control "public, max-age=3600" always;
}
```

Nginx serves static files with byte-range support by default. Verify each publication with `HEAD`, a small range request, and a resumed range request before exposing it to nodes.

## Recommended server hardening

The next server-side phase should publish each archive with a signed or authenticated manifest:

```json
{
  "network": "colosseum",
  "zone": "zone-0-0",
  "height": 9050000,
  "createdAt": "2026-07-16T00:00:00Z",
  "archive": "mainnet-snapshot-9050000.tar.zst",
  "size": 200914328675,
  "sha256": "<hex digest>"
}
```

Recommended publication process:

1. Create the archive under a versioned filename, never the live filename.
2. Fully sync file contents and calculate SHA-256 and exact byte size.
3. Open the snapshot's zone database offline and record its canonical height.
4. Upload the versioned archive and manifest, then verify both from the public endpoint.
5. Atomically switch a small `latest.json` manifest to the new version.
6. Retain the previous version for rollback and active resumable clients.
7. Apply long immutable caching to versioned archives and short caching to `latest.json`.
8. Monitor `HEAD`, `206`, completion rates, bytes served, and checksum failures.

Once this manifest exists, the client should fetch it before deciding to download, skip snapshots that are not newer than the local database, verify available disk against the advertised size, and calculate SHA-256 before extraction. Signing `latest.json` with a release key would prevent the snapshot host alone from authorizing database contents.
