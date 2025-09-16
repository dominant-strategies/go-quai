# Quai Mining RPC Specification

This document defines the JSON-RPC surface that mining pools use to interact with
go-quai. Quai produces blocks roughly every 5 seconds; pools should refresh their
work once per second to keep producing fresh shares and get paid.

## Mining Workflow
- Request a block template for the desired Proof-of-Work algorithm.
- Insert pool-specific data into the coinbase transaction and update the merkle root.
- Run Proof-of-Work on the header using the algorithm specified by the template.
- Submit the fully serialized block through the matching submission method.

## `quai_getBlockTemplate`

### Request
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "quai_getBlockTemplate",
  "params": [
    {
      "rules": ["kawpow"],
      "extranonce1": "00123456",
      "extranonce2": "0011223344556677",
      "extradata": "/abcpool/",
      "coinbase": "0x099f4771cba8d3cef3b3c3cb0a028a0f064dc082"
    }
  ]
}
```

`powType` (supplied inside `rules`) selects the target algorithm. Supported values:
`"kawpow"`, `"sha"`, `"scrypt"`. If multiple values are supplied, the template is built
for the first supported entry.

`extranonce1`, `extranonce2`, and `extradata` are optional request fields that let a pool
ask go-quai to pre-populate the returned coinbase transaction. When supplied:

| Field | Format | Description |
| --- | --- | --- |
| `extranonce1` | Hex string (≤ 4 bytes / 8 hex chars) | Spliced between `coinb1` and `coinb2`. Zero-padded if shorter than 4 bytes. |
| `extranonce2` | Hex string (≤ 8 bytes / 16 hex chars) | Added after `extranonce1` and padded with zeros to 8 bytes when shorter. |
| `extradata` | UTF-8 string (≤ 30 bytes) | Overwrites the first `coinbaseAuxExtraBytesLength` bytes of `coinb2` (the mutable aux data region). |
| `coinbase` | 0x-prefixed 20-byte Quai address | Sets the pending header's primary coinbase. Must be a valid in-scope address for the requesting node. |

go-quai recalculates the coinbase and merkle root inside the template before
returning the response, so miners can hash immediately with the requested values. If
the fields are omitted they default to zero bytes, matching the prior behavior.

When `coinbase` is supplied, the pending header returned by `quai_getBlockTemplate`
already targets that address; pools no longer need to rewrite the header to pay a
specific payout account (subject to scope validation for the node's location).

### Response Example
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "bits": "1b0127c1",
    "coinb1": "02000000010000000000000000000000000000000000000000000000000000000000000000ffffffff6403ca5f3e04fabe6d6d206cdda0b4680e7beefa2db3313c6bd4b38fc30b8cc45d57e4e8507ca1246b55bd040100000004000000002a",
    "coinb2": "00000000000000000000000000000000000000000000000000000000000004d1250569ffffffff01004429353a0000001976a9143da104bf8e6560ba325d4d301e366c9e9a5f70fa88ac00000000",
    "coinbaseAuxExtraBytesLength": 30,
    "curtime": 1761945041,
    "extranonce1Length": 4,
    "extranonce2Length": 8,
    "height": 4087754,
    "merklebranch": [],
    "merkleroot": "6c25e5bdebb5e25a87ed456383589607a8bc35f2afe708ff8e98db018d9be7ed",
    "mintime": 1761945041,
    "noncerange": "00000000ffffffff",
    "previousblockhash": "000000000000d6d9eb436d5330aaea517f4a0fe4886ec67a1d83c057ed67d96d",
    "sigoplimit": 80000,
    "sizelimit": 1000000,
    "target": "000000674a1f0000000000000000000000000000000000000000000000000000",
    "version": 805306368,
    "quairoot": "12abd412abd4",
    "quaiheight": 1234,
  }
}
```

### Response Fields
| Field | Type | Description |
| --- | --- | --- |
| `bits` | string | Compact representation of the current target difficulty. Must not be changed. |
| `coinb1` | string | First half of the serialized coinbase transaction up to (but excluding) `extranonce1`. Supplied by go-quai and must remain unchanged. |
| `coinb2` | string | Remaining bytes of the coinbase transaction after `extranonce2`. Includes coinbase outputs and default padding. Only the first `coinbaseAuxExtraBytesLength` bytes may be replaced with pool metadata; the remainder must stay intact. |
| `coinbaseAuxExtraBytesLength` | number | Length (in bytes) of the mutable prefix at the start of `coinb2` reserved for pool-supplied auxiliary data. |
| `curtime` | number | Current Unix timestamp used when the template was generated. Pools may update it within consensus rules. |
| `extranonce1Length` | number | Expected byte length for `extranonce1`. |
| `extranonce2Length` | number | Expected byte length for `extranonce2`. |
| `height` | number | Block height the miner is targeting. Read-only donor chain information. |
| `merklebranch` | array | Merkle branches (big-endian hex strings) required to recompute the block merkle root once the coinbase is finalized. |
| `merkleroot` | string | Merkle root computed with the provided `coinb1/coinb2` in big endian. Update after customizing the coinbase. |
| `mintime` | number | Earliest valid timestamp for the block. |
| `noncerange` | string | Hex-encoded inclusive nonce range miners may iterate through. |
| `previousblockhash` | string | Hash of the parent block. Must not be changed. |
| `sigoplimit` | number | Maximum allowed sigops count for the block. |
| `sizelimit` | number | Maximum serialized block size. |
| `target` | string | Full 256-bit target corresponding to `bits`. |
| `version` | number | Block header version to use. First byte of version cannot be changed, last three bytes can be changed in compliance with bip320 (i.e asicboost). |
| `quairoot` | string | first 6 bytes of the sealhash without the time, so everytime this changes, new job needs to be sent to the miner|
| `quaiheight` | number | uint64 encoding of the quai zone chain |

`bits`, `height`, `previousblockhash`, `coinb1`, `coinb2` from byte 31 till end and first byte of `version` are signed donor-chain data.
Changing them will result in an invalid block.

### Coinbase Assembly
Construct the coinbase transaction by concatenating:

```
coinbase = coinb1 + extranonce1 + extranonce2 + coinb2
```

- `extranonce1` and `extranonce2` may be chosen by the pool or requested directly via
  `quai_getBlockTemplate`. If they are omitted, go-quai returns zero bytes and miners
  should insert zero bytes matching `extranonce1Length` and `extranonce2Length`. Pools
  typically set `extranonce1`; individual miners supply `extranonce2`. Both values must
  match the advertised byte lengths.
- Ensure that any auxiliary metadata fits within `coinbaseAuxExtraBytesLength`. This
  flexible segment is already allocated inside `coinb2`; replace those bytes as needed
  (for example, to embed a pool identifier). Supplying `extradata` in the request lets
  go-quai pre-populate the segment and merkle root for you.
- After altering the coinbase, recompute the merkle root using the updated coinbase
  hash and the provided `merklebranch`.

Refresh the template frequently (≈ once per second) to get paid for the shares.
If refreshed any slower, will result in stale shares which will not be paid.

### Coinbase Example

Using the template above (`extranonce1Length = 4`, `extranonce2Length = 8`,
`coinbaseAuxExtraBytesLength = 30`):

- `coinb1`  
  `02000000010000000000000000000000000000000000000000000000000000000000000000ffffffff6403ca5f3e04fabe6d6d206cdda0b4680e7beefa2db3313c6bd4b38fc30b8cc45d57e4e8507ca1246b55bd040100000004000000002a`
- Pool-selected `extranonce1`: `00123456`
- Miner-selected `extranonce2`: `1223432122346789`
  - Pool name `/abcpool/` encoded as ASCII hex: `2f616263706f6f6c2f` (follows bip34, but has to be below 30 bytes)
- `coinb2` with pool metadata packed into the 30-byte mutable prefix (padded with zeros):
  `616263706f6f6c000000000000000000000000000000000000000000000004d1250569ffffffff01004429353a0000001976a9143da104bf8e6560ba325d4d301e366c9e9a5f70fa88ac00000000`

Final serialized coinbase transaction:
```
02000000010000000000000000000000000000000000000000000000000000000000000000ffffffff6403ca5f3e04fabe6d6d206cdda0b4680e7beefa2db3313c6bd4b38fc30b8cc45d57e4e8507ca1246b55bd040100000004000000002a0012345612234321223467892f616263706f6f6c2f00000000000000000000000000000000000000000004d1250569ffffffff01004429353a0000001976a9143da104bf8e6560ba325d4d301e366c9e9a5f70fa88ac00000000
```

Include this transaction as the first entry in the block template’s transaction list,
recompute the merkle root using the supplied `merklebranch`, and proceed with header
hashing for the selected Proof-of-Work algorithm.

## Block Submission Methods

Each submission method accepts a single parameter containing the fully serialized
block (header + transactions) as a hex string prefixed with `0x`.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "quai_submitKawpowBlock",
  "params": ["0x...serialized block bytes..."]
}
```

| Algorithm | Method | Notes |
| --- | --- | --- |
| KawPow | `quai_submitKawpowBlock` | Default submission path for KawPow work. |
| SHA | `quai_submitShaBlock` | Use when the template was requested with `powType = "sha"`. |
| Scrypt | `quai_submitScryptBlock` | Use when the template was requested with `powType = "scrypt"`. |

On success the RPC returns a JSON object of the form
`{"hash": "<workshare-or-block-hash>", "number": "<hex-encoded-block-number>"}`.
Errors follow the standard JSON-RPC error object, and miners should retry with a
fresh template if the node reports that
the block is stale or the parent has changed.

Upon successful submission, the node relays the block across the network and credits
the pool according to the block reward encoded in the coinbase transaction.

## Block Reward 

Payment for these shares will happen to Quai coinbase set in the go-quai node.
There is no need to set coinbase in stratum. Quai block reward will follow the current
structure after the soap/kawpow upgrade.

## Reference Implementations

Two public pool codebases already integrate these RPCs. Reviewing their diffs can
speed up custom deployments.

### SHA & Scrypt Reference (ckpool)
- Repository: `https://github.com/jdowning100/ckpool`
- Key updates:
  - Switched JSON-RPC calls from `getblocktemplate` to `quai_getBlockTemplate` and
    added support for selecting `rules` based on the pool’s configured algorithm.
  - Normalized extranonce handling to respect `extranonce1Length`/`extranonce2Length`
    coming from go-quai and padded zero bytes when not set by workers.
  - Updated template parsing to use the `coinb1`/`coinb2` split directly rather than
    reconstructing the coinbase transaction from Bitcoin-style fields.
  - Adjusted block submission paths to call `quai_submitShaBlock` or
    `quai_submitScryptBlock`, wrapping the serialized block hex string with a `0x`
    prefix.
  - Converted target/difficulty helpers to use the `bits` and `target` values issued
    by go-quai so share difficulty matches chain validation.

### KawPow Reference (Ravencoin Stratum Proxy)
- Repository: `https://github.com/gameofpointers/ravencoin-stratum-proxy`
- Relevant commit: see history mentioning “go-quai” or “Quai KawPow support”.
- Key updates:
  - Added a Quai-specific upstream driver that fetches work with
    `quai_getBlockTemplate` (requesting `rules: ["kawpow"]`) and caches the resulting
    template on the same 1-second cadence the daemon expects.
  - Patched the coinbase builder to splice `extranonce1`/`extranonce2` between
    `coinb1` and `coinb2`, and to propagate the recomputed merkle root back into the
    header before hashing.
  - Replaced Ravencoin’s submission endpoint with `quai_submitKawpowBlock`, ensuring
    the stratum proxy forwards the fully serialized block (header + transactions) to
    go-quai.
  - Wired up KawPow header hashing so the mix-hash and nonce returned by miners map to
    the template’s `target` and can be inserted into the header format go-quai expects.

When implementing a custom pool, replicate the above changes: honor the Quai block
template schema, preserve donor-chain fields, and submit completed blocks through the
algorithm-specific RPC entry points described in this spec.
