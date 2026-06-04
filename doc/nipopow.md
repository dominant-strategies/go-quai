# NiPoPoW Proofs for Trust-Reduced Mining Templates

## Status

This document describes the staged NiPoPoW work for `go-quai`.

Current stage: Prime-chain proof foundation.

The current implementation is intentionally read-only and non-consensus. It does not change block validation rules, fork choice, mining behavior, or persisted chain state. Its purpose is to add the Prime-chain proof primitives needed before Zone mining templates can be verified by external pools without trusting the node that produced the template.

## Problem

Mining pools currently need to trust or whitelist nodes that provide block templates. A pool receiving a Zone block template from an untrusted node needs compact evidence that the template is anchored into the canonical Quai hierarchy and backed by sufficient Prime-chain work.

The desired verification shape is:

```text
Zone block template/header
  -> committed/linked by Zone/Region manifest context
Region context
  -> committed/linked by Region/Prime manifest context
Prime context
  -> proven by compact Prime NiPoPoW proof
Sufficient Prime-chain work according to the verifier policy
```

The first implementation stage only provides the bottom layer:

```text
Prime context
  -> compact Prime NiPoPoW proof
```

The Zone/Region wrapper and mining-template integration are later stages.

## Goals

- Add a compact Prime-chain proof representation.
- Build Prime proofs from already-persisted canonical chain data.
- Verify proof structure, header/body binding, interlink roots, linear suffixes, and interlink jumps.
- Verify proof-of-work/rank when the caller supplies the consensus PoW hash function.
- Bound public proof generation with context cancellation and explicit limits.
- Keep the implementation read-only and non-consensus.
- Provide a clean foundation for later Zone -> Region -> Prime template proofs.

## Non-goals for the current stage

- No consensus-rule changes.
- No fork-choice changes.
- No block-template behavior changes.
- No DB writes or recalculation of persisted interlinks.
- No claim that Zone templates are trustlessly verifiable yet.
- No public verifier RPC that could be mistaken for a canonical truth oracle.

## Current code layout

- `core/nipopow/proof.go`
  - `Proof`
  - `VerifyPrimeProof`
  - structural checks for header connectivity and interlink commitments

- `core/nipopow/build.go`
  - `PrimeProofSource`
  - `BuildPrimeProof`
  - `BuildPrimeProofWithContext`
  - bounded canonical-chain walking and proof compression

- `core/nipopow/pow.go`
  - `PrimePoWVerifier`
  - `VerifyPrimeProofWithPoW`
  - `VerifyPrimeProofWork`
  - `CalcRank`

- `core/nipopow/score.go`
  - `ScorePrimeProof`
  - `ComparePrimeProofs`
  - `CompareProofScores`

- `core/headerchain_nipopow.go`
  - read-only bridge from canonical chain storage into the proof builder

- `core/core_nipopow.go`
  - Core-level proof access

- `internal/quaiapi/backend.go`
  - backend interface extension

- `quai/api_backend.go`
  - concrete backend bridge

- `internal/quaiapi/quai_api.go`
  - bounded Prime-only `quai_getNiPoPoWProof` RPC access

## Safety model

The Prime proof path is designed as read-only infrastructure.

Important safety properties:

1. The proof builder reads persisted chain data only.
2. It does not call interlink calculation paths that can mutate storage.
3. Genesis is handled explicitly so the builder does not attempt to read a nonexistent parent.
4. Public proof construction is bounded by:
   - maximum chain walk length
   - maximum proof header count
   - maximum `m`
   - context cancellation
5. The structural verifier checks:
   - non-nil proof and headers
   - valid `m`
   - sufficient suffix length
   - anchor consistency
   - WorkObject header/body binding
   - interlink root consistency
   - forward-only interlink jumps
   - linear final suffix
6. PoW verification is separated from structural verification because it requires access to the consensus PoW hash function.
7. Generic public proof verification is intentionally not exposed as an RPC endpoint. A public verifier that accepts arbitrary proofs without canonical-chain context can be misused as a misleading truth oracle.

## Verification levels

### Level 1: package and integration tests

The first stage should pass:

```bash
go test ./core/nipopow ./core ./internal/quaiapi ./quai -count=1
go test ./... -run TestNonExistentCompileOnly -count=0
go vet ./core/nipopow ./internal/quaiapi ./quai
go build ./cmd/go-quai
```

### Level 2: real-chain validation

Before relying on this for mining or public production behavior, run the proof builder against real or snapshot chain data:

- recent Prime blocks
- older Prime blocks
- short ranges
- long ranges near the configured limit
- genesis / early-chain edge cases
- recent tips
- malformed/tampered proof inputs

Record:

- proof generation time
- proof size
- memory behavior
- verification result
- failure mode for malformed requests

### Level 3: adversarial validation

Before treating the proof system as security-critical, add fuzz/property tests and adversarial cases:

- tampered headers
- swapped WorkObject bodies
- bad interlink roots
- backward/no-progress interlink jumps
- disconnected suffixes
- stale tips
- competing proofs with different score vectors
- anchors not in the tip ancestry
- oversized requested ranges
- invalid `m`

### Level 4: mining-template readiness

Mining-template use requires the later stages:

1. Zone -> Region manifest wrapper.
2. Region -> Prime manifest wrapper.
3. Combined proof object.
4. Pool/client verifier.
5. Optional proof delivery through `quai_getBlockTemplate`.
6. Shadow-mode deployment before pools depend on the result economically.

## Recommended upstream PR sequence

Even if the implementation is completed internally before review, the upstream GitHub changes should remain split into small, reviewable PRs.

### PR 1: Prime NiPoPoW core

Scope:

- `core/nipopow`
- read-only HeaderChain/Core integration
- bounded Prime proof RPC, if accepted as an experimental proof-fetch endpoint
- tests
- this documentation

This PR should be mergeable on its own and should not claim to solve trustless mining templates yet.

Suggested title:

```text
feat(core): add read-only Prime NiPoPoW proof engine
```

### PR 2: Zone/Region/Prime manifest wrapper

Scope:

- prove Zone context is committed into Region context
- prove Region context is committed into Prime context
- combine wrapper proof with Prime NiPoPoW proof
- verifier for the combined hierarchical proof

Suggested title:

```text
feat(core): add hierarchical NiPoPoW manifest wrapper
```

### PR 3: Mining-template and pool integration

Scope:

- optional proof field in `quai_getBlockTemplate`
- config/feature flag
- request limits and timeout behavior
- pool/client verifier documentation or minimal library/example
- shadow-mode guidance

Suggested title:

```text
feat(miner): expose optional NiPoPoW proof with block templates
```

## Why not one large PR?

A single PR containing Prime proof logic, manifest wrappers, block-template changes, and pool verifier code would be harder to review and riskier to merge. Splitting the work keeps each layer independently testable:

1. Prime proof correctness.
2. Hierarchical anchoring correctness.
3. Mining-template integration correctness.

The implementation can still be developed end-to-end internally before opening the final upstream PRs. The split is for reviewability, risk control, and easier rollback.

## Mainnet-readiness bar

Before this should influence mainnet mining decisions:

- all package and integration tests pass
- real-chain validation completed
- adversarial/fuzz validation completed
- proof generation remains bounded under bad inputs
- no DB writes from proof paths
- no default consensus or mining behavior change
- feature flag or opt-in API behavior
- pool/client verifier tested independently from the prover
- shadow-mode operation before economic reliance
- rollback path documented

## Short version

The current implementation is the Prime-chain foundation. It is designed to be safe, read-only, and independently testable. The full trust-reduced mining-template flow requires two additional layers: a Zone/Region/Prime manifest wrapper and an opt-in block-template/pool verifier integration.
