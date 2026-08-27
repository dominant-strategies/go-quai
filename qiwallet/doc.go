// Package qiwallet is a reference library for building Qi wallets.
//
// Every Qi wallet must independently solve a set of sharp-edged problems
// that are easy to get subtly wrong. This package provides vetted building
// blocks for each of them:
//
//   - Denomination decomposition (DecomposeGreedy, DecomposeRandom): Qi
//     values are carried in fixed-denomination notes. Deterministic greedy
//     decomposition leaks information: observers can separate change from
//     payment by shape. DecomposeRandom produces value-preserving randomized
//     shapes.
//
//   - Coin selection (SelectUTXOs): consensus forbids combining smaller
//     denominations into larger ones (core.CheckDenominations), so outputs
//     must be constructible from the chosen inputs by breaking notes
//     downward, like a cashier making change. SelectUTXOs also prefers
//     notes that are close to being trimmed (expiry) and accounts for the
//     fee, which is simply inputs minus outputs.
//
//   - Transaction assembly (BuildQiTx): consensus rejects a transaction in
//     which any output address equals another output address or any input
//     address, so every note needs a fresh address. Output order is
//     shuffled so change position leaks nothing.
//
//   - Signing (SignQiTx): a Qi transaction carries one aggregate Schnorr
//     signature over all inputs. Multi-input transactions require the
//     MuSig2 protocol across the input keys, mirroring consensus
//     verification exactly (unsorted key aggregation, BIP-340 Schnorr for
//     the single-input case). Nonces are generated fresh from crypto/rand
//     for every signing session; MuSig2 nonces must NEVER be derived
//     deterministically or reused - doing so can leak the private key.
//
//   - Address grinding (GrindQiAddress): a Qi address must land in the
//     wallet's zone (first byte) and in Qi ledger scope (high bit of the
//     second byte), which takes ~512 key derivations on average.
//
//   - Expiry tracking (ExpiryHeight, RefreshCandidates): denominations 0-5
//     are trimmed (burned) 2-12 weeks after creation. Wallets must refresh
//     or spend them before expiry or the funds are destroyed.
//
// Operational notes: the default node mempool drops Qi transactions after
// 30 minutes, so wallets should rebroadcast unconfirmed transactions well
// inside that window, and use the txpool_qiTxStatus RPC to detect drops.
// Denomination-consolidation transactions (which fail the no-combining
// rule) are only minable in the first Qi slot of a block and compete in a
// per-block fee auction; expect them to confirm more slowly.
package qiwallet
