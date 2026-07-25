# Qi off-chain contract and payment layer

This document describes the design of Qi payment channels, PTLCs and routed
payments, the consensus primitives they need, and the limits of the approach.

## Summary

Qi verifies exactly one aggregate Schnorr signature per transaction over all
input public keys. That single fact allows a large class of two-party
contracts to be expressed with no script system at all: participants are
encoded in a MuSig2 aggregate key, conditions in adaptor signatures, and time
in locktimes. Every such contract settles on chain as an ordinary-looking
payment to an ordinary-looking address, so expressiveness costs nothing in
fungibility or privacy.

Concretely this branch adds:

| Layer | Package | Status |
| --- | --- | --- |
| Adaptor signatures (single key and MuSig2) | `crypto/adaptor` | implemented, tested |
| Transaction locktime, output locks | `core`, `params` | implemented, fork gated |
| Channels, PTLCs, routes, invoices | `qichannel` | implemented, tested |
| Onion routing, gossip, pathfinding | — | not implemented |
| Quai-side contract channels | — | not implemented |

## Consensus primitives

Two fork-gated changes (`params.QiUserLockForkBlock`) provide the timing
primitives. They are distinct and both are needed.

**Output locks** (`TxOut.Lock`) delay spending a UTXO that already exists.
This was already stored and enforced at spend time; the change permits users
to set it. It gives vaults, dead-man switches and delayed-withdrawal
constructions. Locked outputs are restricted to denominations above
`MaxTrimDenomination` because the trimmer skips locked entries, so a small
locked note would otherwise never be trimmed and would bloat the UTXO set
permanently.

**Transaction locktime** delays *a transaction*. It is encoded as an 8-byte
big-endian height in the Qi `Data` field, which is already covered by the
signing digest (so it is not malleable) and already carries typed payloads at
other lengths (20 bytes = wrapping, 22 = conversion). Encoding it there rather
than as a new struct field avoids any protobuf, RLP or transaction-hash
change, and therefore any change to how existing transactions serialize or
hash.

### Why output locks alone are not enough

It is tempting to think channels only need output locks. They do not, and the
distinction is easy to miss:

- An output lock says *this coin cannot move until height H*.
- A locktime says *this transaction cannot confirm until height H*.

Every state of a channel spends the **same funding output**. If timing were a
property of that output, every state would inherit identical timing, and there
would be no way to make the newest state confirm before a stale one. The
security of the whole construction rests on that ordering, so a transaction
locktime is required. An earlier iteration of this work shipped output locks
alone and would not, in fact, have supported channels.

## Channel construction

A channel is one funding UTXO paying to `addr(MuSig2Agg(A, B))`. Because the
aggregate key is determined by the participants' keys, one party grinds its
key until the aggregate hashes into the zone's Qi scope (`GrindFundingKey`,
about 512 attempts).

States are ordered by **decrementing locktime**. State `i` settles with a
transaction whose locktime is:

```
maturity(i) = OpenHeight + (MaxStates - i) * SettlementDelta
```

Later states mature earlier. An honest party publishing the newest state
always beats any stale state its counterparty holds, because the stale state
is not yet valid.

This yields two budgets that callers must respect, both exposed by the API:

- **Watch window** (`WatchWindow`): `SettlementDelta` blocks, the interval in
  which a unilateral close of state `i` must be published. After it, state
  `i-1` also matures and the outcome becomes a race.
- **Lifetime** (`ExpiryHeight`) and **update budget**
  (`RemainingUpdates`): `MaxStates` updates over `MaxStates *
  SettlementDelta` blocks. The channel must be closed or rolled over before
  expiry.

Cooperative closes carry no locktime and confirm immediately, so a
well-behaved channel never waits. The defaults are a one-day watch window and
a one-year lifetime, giving 365 updates; trading window for updates is the
central tuning decision.

### What is not constructible

**Perpetual (penalty) channels.** Lightning's revocation model punishes a
cheater by giving the victim an immediate spending path while the cheater's is
delayed. That asymmetry requires two spending branches from one output with
different conditions, i.e. a script system. With one output and one lock,
both parties face identical timing and punishment degenerates into a race.
Channels here are therefore time bounded by construction.

An `ANYPREVOUT`-style sighash mode would allow eltoo-style perpetual channels
without penalties, and is the natural follow-on if unbounded channel lifetime
becomes important.

## PTLCs and routing

A PTLC holds an amount claimable by revealing the discrete log of a payment
point. The claim transaction carries no locktime and is gated only by an
adaptor signature; the refund carries the deadline as its locktime. Publishing
a claim necessarily reveals the secret, which is what lets the upstream hop
claim in turn — atomicity with no hash preimages and no scripts.

`crypto/adaptor` builds MuSig2 adaptor signatures on top of btcd unmodified.
btcd computes the aggregate nonce as `R = R₁ + b·R₂` and challenges over
`x(R)`, so adding the adaptor point to the first nonce point makes every
signer challenge over the shifted nonce while their secret nonces still sum to
the unshifted one; the combined signature is then short by exactly the secret.
Since BIP-340 carries only `x(R)`, verification lifts it to even Y, so when
the shifted nonce is odd the signer negates its nonce and completion subtracts
rather than adds. Key aggregation reuses `musig2.AggregateKeys(keys, false)`,
so completed signatures verify under precisely the key consensus checks.

Routes use a **deadline staircase**: the hop nearest the payee has the
earliest deadline and each hop upstream gets `DefaultHopDelta` more blocks, so
an intermediary always has time to claim upstream after being claimed
downstream. Because channels are time bounded, `Route.FitsChannels` rejects
routes whose deadlines fall outside any hop's remaining channel lifetime —
routing capacity decays as channels approach expiry, which is a scheduling
concern Lightning does not have.

Every hop is locked to a **different blinded point** (`T + rᵢ·G`). Lightning's
HTLCs reuse one payment hash at every hop, letting colluding intermediaries
correlate a route end to end and enabling wormhole attacks; PTLCs remove that
by construction.

## Cross-ledger routing

The routing layer's atomicity primitive is only "reveal a scalar for a
point", which is not Qi-specific. On Qi it is enforced by adaptor signatures;
on the Quai EVM ledger the same condition can be enforced by a contract that
checks `T = t·G`. A single network can therefore carry payments whose hops
traverse Qi channels and Quai contract-channels interchangeably, with one
onion protocol and one invoice format — hence the `Ledger` field on
`Invoice`. Because an EVM adjudicator can implement challenge periods and
penalties, Quai-side channels can be perpetual even while Qi-side channels are
time bounded.

This makes atomic Qi↔Quai payments possible off chain. That is a secondary
market between existing holders and does not interact with protocol
conversion, which changes supply and is the monetary-policy instrument the
kQuai controller reads; the two layers are complementary rather than
competing.

## Not implemented

- **Onion routing.** A Sphinx-style packet format is needed so intermediaries
  learn only their predecessor and successor. Standard and well specified,
  but not written here.
- **Gossip and pathfinding.** Public routing nodes must announce channels by
  pointing at a funding outpoint (verifiable through `quai_getUTXO`), which
  trades that channel's privacy for routability. Unannounced channels remain
  fully private.
- **Quai-side contract channels**, and the liquidity/fee economics of a
  routing network (jamming, probing, rebalancing), which are unsolved in
  Lightning too.
- **Network integration.** Nothing here is wired into the node; the packages
  are libraries. A channel daemon holding state, watching heights and
  publishing at maturity is future work.
