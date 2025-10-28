# Stratum Server

This package implements a Stratum v1 compatible TCP server for mining to a Quai node. It allows standard mining hardware and software to submit workshares using auxiliary proof-of-work (AuxPow).

## Overview

The stratum server translates between the standard Stratum mining protocol and Quai's workshare system. Miners connect using standard stratum-compatible software and receive jobs derived from pending Quai headers. Valid shares that meet the workshare difficulty are submitted to the node.

**Note:** This is a **solo mining** setup. There is no pool fee - miners receive 100% of any block rewards directly to their configured address. Each miner works independently against the network difficulty.

## Enabling the Stratum Server

Add these flags when starting your go-quai node:

```bash
go-quai start \
  --node.stratum-enabled \
  --node.stratum-addr "0.0.0.0:3333"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--node.stratum-enabled` | `false` | Enable the stratum TCP endpoint |
| `--node.stratum-addr` | `0.0.0.0:3333` | Listen address and port for stratum connections |

## Supported Mining Algorithms

The server supports multiple proof-of-work algorithms via the password field during authorization:

| Password | Algorithm | Compatible Hardware |
|----------|-----------|---------------------|
| `sha` | SHA-256 | SHA-256 ASICs |
| `scrypt` | Scrypt | Scrypt ASICs |
| `kawpow` | KawPow | GPUs |

If no password is provided, defaults to `sha` (SHA-256).

## Miner Configuration

Configure your miner with the following settings:

| Setting | Value |
|---------|-------|
| **Pool URL** | `stratum+tcp://<node-ip>:3333` |
| **Username** | Your payout address (Quai or Qi) |
| **Password** | Algorithm identifier (`sha`, `scrypt`, `kawpow`) |

### Payout Address

You can mine to either a **Quai** or **Qi** address:

- **Quai address**: Starts with `0x` (e.g., `0x1234...`) - rewards paid in Quai
- **Qi address**: Starts with `0x` but uses a different address space - rewards paid in Qi

Simply set your preferred address as the username when connecting.

### Example: CGMiner / BFGMiner (SHA-256)

```bash
# Mining to a Quai address
cgminer -o stratum+tcp://127.0.0.1:3333 -u 0xYourQuaiAddress -p sha

# Mining to a Qi address
cgminer -o stratum+tcp://127.0.0.1:3333 -u 0xYourQiAddress -p sha
```

### Example: Scrypt Miner

```bash
cgminer --scrypt -o stratum+tcp://127.0.0.1:3333 -u 0xYourAddress -p scrypt
```

### Example: lolMiner (KawPow)

```bash
lolminer --algo KAWPOW --pool stratum+tcp://127.0.0.1:3333 --user 0xYourAddress --pass kawpow
```

## Stratum Protocol Support

The server implements these Stratum v1 methods:

| Method | Description |
|--------|-------------|
| `mining.subscribe` | Initialize connection, receive extranonce1 |
| `mining.authorize` | Authenticate with address and algorithm |
| `mining.configure` | Configure version rolling (ASICBoost) |
| `mining.extranonce.subscribe` | Subscribe to extranonce updates |
| `mining.submit` | Submit a found share |

### Notifications Sent to Miners

| Method | Description |
|--------|-------------|
| `mining.set_difficulty` | Update target difficulty |
| `mining.set_version_mask` | Version rolling mask (if enabled) |
| `mining.notify` | New job notification |

## Version Rolling (ASICBoost)

The server supports version rolling for compatible ASICs. When a miner sends `mining.configure` with version-rolling capability, the server will:

1. Acknowledge the capability
2. Send the allowed version mask
3. Accept submissions with rolled version bits

## Logging

Stratum server logs are written to `nodelogs/stratum.log`. Log entries include:

- Connection events
- Job notifications
- Share submissions (accepted/rejected)
- Difficulty adjustments
- Errors and warnings

Set the log level using the global `--global.log-level` flag.

## Architecture

```
┌─────────────┐     Stratum v1      ┌─────────────────┐
│   Miner     │ ◄────────────────►  │  Stratum Server │
│  (ASIC/GPU) │    TCP :3333        │   (this pkg)    │
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

1. Miner connects and subscribes (`mining.subscribe`)
2. Miner authorizes with address and chain (`mining.authorize`)
3. Server fetches pending header from node backend
4. Server constructs stratum job from AuxPow data
5. Server sends difficulty and job to miner
6. Miner finds valid nonce and submits (`mining.submit`)
7. Server validates share against workshare target
8. If valid, server submits workshare to node
9. Server sends new job to miner

Jobs are refreshed every second to keep miners on the latest work.

## Difficulty

The stratum difficulty is derived from the Quai workshare difficulty for the selected algorithm:

- **SHA-256**: `minerDiff = ShaDiff / 2^32`
- **Scrypt**: `minerDiff = ScryptDiff / 65536`
- **KawPow**: `minerDiff = KawpowDiff / 2^32`

## Troubleshooting

### "no pending header" errors
The node may not have synced or there's no pending work. Ensure your node is fully synced and connected to the network.

### Miner shows "authorization failed"
Verify your Quai address format is correct.

### High reject rate
- Check that the password matches your mining hardware's algorithm
- Ensure your miner supports the difficulty being sent
- Check `nodelogs/stratum.log` for specific error messages

### Connection timeout
The server sets a 60-second deadline on connections. Ensure your miner sends regular requests or the connection may be closed.

## Web Dashboard

A web dashboard is available for monitoring your node and stratum server.

### Enabling the Dashboard

```bash
go-quai start \
  --node.stratum-enabled \
  --node.dashboard-enabled \
  --node.dashboard-addr "0.0.0.0:8080"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--node.dashboard-enabled` | `false` | Enable the web dashboard |
| `--node.dashboard-addr` | `0.0.0.0:8080` | Listen address for dashboard |

Once enabled, open `http://localhost:8080` in your browser to view:
- Connected workers and their hashrates
- Share submission statistics
- Blocks found
- Network peer visualization (globe)
- Sync status

## Future Work

This stratum server is currently minimal and designed for solo mining. Planned improvements include:

- **Pool mode**: Optional pooled mining with configurable payout schemes
- **Vardiff**: Dynamic difficulty adjustment based on miner hashrate
- **Desktop app**: Native application using Wails for easier deployment
