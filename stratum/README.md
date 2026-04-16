# Stratum Server

This package implements a Stratum v1 compatible TCP server for mining to a Quai node. It allows standard mining hardware and software to submit workshares using auxiliary proof-of-work (AuxPow).

## Overview

The stratum server translates between the standard Stratum mining protocol and Quai's workshare system. Miners connect using standard stratum-compatible software and receive jobs derived from pending Quai headers. Valid shares that meet the workshare difficulty are submitted to the node.

**Note:** This is a **solo mining** setup. There is no pool fee - miners receive 100% of any block rewards directly to their configured address.

## Quick Start

Add these flags when starting your go-quai node:

```bash
go-quai start \
  --node.stratum-enabled \
  --node.stratum-sha-addr "0.0.0.0:3333" \
  --node.stratum-scrypt-addr "0.0.0.0:3334" \
  --node.stratum-kawpow-addr "0.0.0.0:3335" \
  --node.stratum-api-addr "0.0.0.0:3336" \
  --node.stratum-name "my-node"
```

## Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--node.stratum-enabled` | `false` | Enable stratum endpoints |
| `--node.stratum-sha-addr` | `0.0.0.0:3333` | Listen address for SHA256 miners |
| `--node.stratum-scrypt-addr` | `0.0.0.0:3334` | Listen address for Scrypt miners |
| `--node.stratum-kawpow-addr` | `0.0.0.0:3335` | Listen address for KawPoW miners |
| `--node.stratum-api-addr` | `0.0.0.0:3336` | Listen address for HTTP API |
| `--node.stratum-name` | `""` | Unique node identifier (for multi-node pools) |
| `--node.stratum-pool-tag` | `""` | Optional tagging the shares/blocks with a string |


## Supported Algorithms

Each algorithm has its own dedicated port:

| Port | Algorithm | Compatible Hardware |
|------|-----------|---------------------|
| 3333 | SHA-256 | SHA-256 ASICs |
| 3334 | Scrypt | Scrypt ASICs |
| 3335 | KawPoW | GPUs |

Connect your miner to the appropriate port for your hardware.

## Miner Configuration

| Setting | Value |
|---------|-------|
| **Pool URL** | `stratum+tcp://<node-ip>:<port>` |
| **Username** | Your payout address (Quai or Qi) |
| **Password** | Optional: `d=<difficulty>` (see below) |

### Payout Address

You can mine to either a **Quai** or **Qi** address:
- **Quai address**: Rewards paid in Quai
- **Qi address**: Rewards paid in Qi

### Password Field (Optional)

The password field accepts optional parameters:

| Parameter | Description |
|-----------|-------------|
| `d=<difficulty>` | Set custom share difficulty |
| `frequency=<seconds>` | Set job update frequency |

Combine multiple parameters with `.` separator:
- `d=1000` - Submit shares at difficulty 1000
- `d=500.frequency=2` - Difficulty 500, jobs every 2 seconds
- `x` or empty - Use default workshare difficulty

**Note:** Custom difficulty only affects share submission rate for liveness tracking. Only shares meeting the network workshare difficulty are submitted to the node for rewards.

### Example: SHA-256 Mining

```bash
# Connect to SHA256 port (3333)
cgminer -o stratum+tcp://127.0.0.1:3333 -u 0xYourQuaiAddress -p x

# With custom difficulty
cgminer -o stratum+tcp://127.0.0.1:3333 -u 0xYourQuaiAddress -p d=1000
```

### Example: Scrypt Mining

```bash
# Connect to Scrypt port (3334)
cgminer --scrypt -o stratum+tcp://127.0.0.1:3334 -u 0xYourAddress -p x
```

### Example: KawPoW Mining (GPU)

```bash
# Connect to KawPoW port (3335)
lolminer --algo KAWPOW --pool stratum+tcp://127.0.0.1:3335 --user 0xYourAddress --pass x

# With custom difficulty for faster share feedback
lolminer --algo KAWPOW --pool stratum+tcp://127.0.0.1:3335 --user 0xYourAddress --pass d=100
```

## Stratum Protocol

### Supported Methods

| Method | Description |
|--------|-------------|
| `mining.subscribe` | Initialize connection, receive extranonce1 |
| `mining.authorize` | Authenticate with address |
| `mining.configure` | Configure version rolling (ASICBoost) |
| `mining.extranonce.subscribe` | Subscribe to extranonce updates |
| `mining.submit` | Submit a found share |

### Notifications

| Method | Description |
|--------|-------------|
| `mining.set_difficulty` | Update target difficulty |
| `mining.set_version_mask` | Version rolling mask |
| `mining.notify` | New job notification |

## Version Rolling (ASICBoost)

The server supports version rolling for compatible ASICs. When a miner sends `mining.configure` with version-rolling capability, the server acknowledges and sends the allowed version mask.

## Difficulty

Stratum difficulty is derived from Quai workshare difficulty:

- **SHA-256**: `minerDiff = ShaDiff / 2^32`
- **Scrypt**: `minerDiff = ScryptDiff / 65536`
- **KawPoW**: `minerDiff = KawpowDiff / 2^32`

## Pool HTTP API

The stratum server exposes an HTTP API for statistics and dashboard integration.

### Endpoints

#### Pool-wide

| Endpoint | Description |
|----------|-------------|
| `GET /api/pool/stats` | Pool overview with hashrate, workers, shares |
| `GET /api/pool/blocks` | Blocks found by this node |
| `GET /api/pool/workers` | All connected workers |
| `GET /api/pool/shares` | Share history with luck stats |

#### Miner-specific

| Endpoint | Description |
|----------|-------------|
| `GET /api/miner/{address}/stats` | Stats for a specific address |
| `GET /api/miner/{address}/workers` | Workers for an address |

#### Real-time

| Endpoint | Description |
|----------|-------------|
| `WS /api/ws` | WebSocket for live updates (1s interval) |
| `GET /health` | Health check |

#### Standard Pool Format

These endpoints follow common pool API conventions:

| Endpoint | Description |
|----------|-------------|
| `GET /api/stats` | Standard pool stats format |
| `GET /api/miners` | All miners list |
| `GET /api/blocks` | Block history |

### Example Response

**GET /api/pool/stats**
```json
{
  "nodeName": "my-node",
  "workersTotal": 5,
  "workersConnected": 3,
  "hashrate": 1250000000000,
  "sharesValid": 1234,
  "sharesStale": 12,
  "sharesInvalid": 2,
  "blocksFound": 1,
  "uptime": 3600.5,
  "sha": {
    "hashrate": 500000000000,
    "workers": 2
  },
  "scrypt": {
    "hashrate": 250000000,
    "workers": 1
  },
  "kawpow": {
    "hashrate": 749750000000,
    "workers": 2
  }
}
```

## Multi-Node Deployments

For pools with multiple stratum nodes, use **quai-pool-api** to aggregate statistics:

```
┌─────────────────┐
│ quai-depool-ui  │  React Dashboard
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  quai-pool-api  │  Aggregation Service
└────────┬────────┘
         │
    ┌────┴────┐
    ▼         ▼
┌───────┐ ┌───────┐
│go-quai│ │go-quai│  Stratum Nodes
│ node1 │ │ node2 │
└───────┘ └───────┘
```

Each node should have a unique `--node.stratum-name` for identification in the aggregated dashboard.

See the **quai-pool-api** repository for setup instructions.

## Logging

Stratum logs are written to `nodelogs/stratum.log`. Set log level with `--global.log-level`.

Log entries include:
- Connection events
- Job notifications
- Share submissions (accepted/rejected)
- Difficulty adjustments

## Troubleshooting

### "no pending header" errors
The node may not be synced. Ensure your node is fully synced and connected to the network.

### "authorization failed"
Verify your Quai/Qi address format is correct.

### High reject rate
- Ensure you're connecting to the correct port for your hardware
- Verify miner supports the difficulty being sent
- Check `nodelogs/stratum.log` for errors

### Connection timeout
The server uses TCP keepalive. Stale connections are detected when writes fail.

## Architecture

```
┌─────────────┐     Stratum v1      ┌─────────────────┐
│   Miner     │ ◄────────────────►  │  Stratum Server │
│  (ASIC/GPU) │    TCP :3333-3335   │   (this pkg)    │
└─────────────┘                     └────────┬────────┘
                                             │
                                             │ GetPendingHeader()
                                             │ ReceiveMinedHeader()
                                             ▼
                                    ┌─────────────────┐
                                    │   Quai Node     │
                                    │    Backend      │
                                    └─────────────────┘
```

## Job Flow

1. Miner connects to algorithm-specific port and subscribes (`mining.subscribe`)
2. Miner authorizes with address (`mining.authorize`)
3. Server fetches pending header from node
4. Server constructs stratum job from AuxPow data
5. Server sends difficulty and job to miner
6. Miner finds valid nonce and submits (`mining.submit`)
7. Server validates share against workshare target
8. If valid, server submits workshare to node
9. Server sends new job to miner

Jobs refresh every second to keep miners on latest work.
