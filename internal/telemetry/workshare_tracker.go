package telemetry

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

type algoKey string

const (
	algoProgpow algoKey = "progpow"
	algoKawpow  algoKey = "kawpow"
	algoSHA     algoKey = "sha"
	algoScrypt  algoKey = "scrypt"

	wsLRUSize = 4096
)

// WorkshareRecord keeps the header and firstSeen time.
type WorkshareRecord struct {
	Header    *types.WorkObjectHeader
	FirstSeen time.Time
}

// counters holds aggregate and per-algorithm counters.
type counters struct {
	// Mined locally
	minedTotal   atomic.Uint64
	minedProgpow atomic.Uint64
	minedKawpow  atomic.Uint64
	minedSHA     atomic.Uint64
	minedScrypt  atomic.Uint64
	// Received from network
	receivedTotal   atomic.Uint64
	receivedProgpow atomic.Uint64
	receivedKawpow  atomic.Uint64
	receivedSHA     atomic.Uint64
	receivedScrypt  atomic.Uint64
	// Candidate in blocks (totals across all blocks processed)
	candidateTotal   atomic.Uint64
	candidateProgpow atomic.Uint64
	candidateKawpow  atomic.Uint64
	candidateSHA     atomic.Uint64
	candidateScrypt  atomic.Uint64
	// Candidate in blocks that were produced locally
	candidateMineTotal   atomic.Uint64
	candidateMineProgpow atomic.Uint64
	candidateMineKawpow  atomic.Uint64
	candidateMineSHA     atomic.Uint64
	candidateMineScrypt  atomic.Uint64
}

var (
	wsCtr counters

	// localShares holds hashes of workshares produced locally (received from miner API).
	localShares = make(map[common.Hash]algoKey, 1024)

	// Stage LRUs
	initOnce     sync.Once
	minedLRU     *lru.Cache[common.Hash, *WorkshareRecord]
	receivedLRU  *lru.Cache[common.Hash, *WorkshareRecord]
	candidateLRU *lru.Cache[common.Hash, *WorkshareRecord]
)

// limitEntries returns at most n entries from es (generic helper).
func limitEntries[T any](es []T, n int) []T {
	if n <= 0 {
		return nil
	}
	if len(es) > n {
		return es[:n]
	}
	return es
}

func ensureLRUs() {
	initOnce.Do(func() {
		minedLRU, _ = lru.New[common.Hash, *WorkshareRecord](wsLRUSize)
		receivedLRU, _ = lru.New[common.Hash, *WorkshareRecord](wsLRUSize)
		candidateLRU, _ = lru.New[common.Hash, *WorkshareRecord](wsLRUSize)
	})
}

func algoFromHeader(h *types.WorkObjectHeader) algoKey {
	if h == nil || h.AuxPow() == nil {
		return algoProgpow
	}
	switch h.AuxPow().PowID() {
	case types.Kawpow:
		return algoKawpow
	case types.SHA_BTC, types.SHA_BCH:
		return algoSHA
	case types.Scrypt:
		return algoScrypt
	default:
		return algoProgpow
	}
}

func bumpByAlgo(which algoKey, total, progpow, kawpow, sha, scrypt *atomic.Uint64) {
	total.Add(1)
	switch which {
	case algoProgpow:
		progpow.Add(1)
	case algoKawpow:
		kawpow.Add(1)
	case algoSHA:
		sha.Add(1)
	case algoScrypt:
		scrypt.Add(1)
	}
}

func addRecord(l *lru.Cache[common.Hash, *WorkshareRecord], h *types.WorkObjectHeader, firstSeen time.Time) {
	if h == nil || l == nil {
		return
	}
	rec := &WorkshareRecord{Header: h, FirstSeen: firstSeen}
	l.Add(h.Hash(), rec)
}

func deleteFrom(l *lru.Cache[common.Hash, *WorkshareRecord], hash common.Hash) {
	if l == nil {
		return
	}
	l.Remove(hash)
}

// RecordMined increments counters and adds to "mined" LRU (header-only available here).
func RecordMined(h *types.WorkObjectHeader) {
	RecordMinedHeader(h)
}

// RecordMinedHeader increments counters and adds to "mined" LRU.
func RecordMinedHeader(h *types.WorkObjectHeader) {
	if h == nil {
		return
	}
	ensureLRUs()

	algo := algoFromHeader(h)
	bumpByAlgo(algo, &wsCtr.minedTotal, &wsCtr.minedProgpow, &wsCtr.minedKawpow, &wsCtr.minedSHA, &wsCtr.minedScrypt)

	addRecord(minedLRU, h, time.Now())

	log.Global.WithFields(log.Fields{
		"type":   "workshare.mined",
		"algo":   string(algo),
		"hash":   h.Hash(),
		"totals": snapshot(),
	}).Info("Workshare mined locally")
}

// RecordReceived keeps header-only support.
func RecordReceived(h *types.WorkObjectHeader) {
	RecordReceivedHeader(h)
}

// RecordReceivedHeader moves header to "received" LRU.
func RecordReceivedHeader(h *types.WorkObjectHeader) {
	if h == nil {
		return
	}
	ensureLRUs()

	algo := algoFromHeader(h)
	bumpByAlgo(algo, &wsCtr.receivedTotal, &wsCtr.receivedProgpow, &wsCtr.receivedKawpow, &wsCtr.receivedSHA, &wsCtr.receivedScrypt)

	addRecord(receivedLRU, h, time.Now())

	log.Global.WithFields(log.Fields{
		"type":   "workshare.received",
		"algo":   string(algo),
		"hash":   h.Hash(),
		"totals": snapshot(),
	}).Info("Workshare received from network")
}

// RemoveMinedShare removes a locally mined share from tracking when broadcast
func RemoveMinedShare(hash common.Hash) {
	deleteFrom(minedLRU, hash)
}

// RemoveCandidateShare removes a candidate share from tracking when included in a block
func RemoveCandidateShare(hash common.Hash) {
	deleteFrom(candidateLRU, hash)
}

// IsLocalShare returns true if the given hash is a locally produced workshare.
func IsLocalShare(hash common.Hash) bool {
	_, ok := localShares[hash]
	return ok
}

// RecordCandidateHeader moves the header to the "candidate" LRU.
func RecordCandidateHeader(h *types.WorkObjectHeader) {
	if h == nil {
		return
	}
	ensureLRUs()

	// Preserve FirstSeen timestamp from earlier stages
	firstSeen := time.Now()
	if rec, ok := receivedLRU.Get(h.Hash()); ok && rec != nil {
		firstSeen = rec.FirstSeen
		deleteFrom(receivedLRU, h.Hash())
	}

	addRecord(candidateLRU, h, firstSeen)
}

// RecordBlockInclusions updates inclusion counters and logs per-block summary.
func RecordBlockInclusions(blockHash common.Hash, perAlgoTotal map[string]int, perAlgoMine map[string]int) {
	inc := func(algo algoKey, n int, total, progpow, kawpow, sha, scrypt *atomic.Uint64) {
		if n <= 0 {
			return
		}
		total.Add(uint64(n))
		switch algo {
		case algoProgpow:
			progpow.Add(uint64(n))
		case algoKawpow:
			kawpow.Add(uint64(n))
		case algoSHA:
			sha.Add(uint64(n))
		case algoScrypt:
			scrypt.Add(uint64(n))
		}
	}

	// Update global totals for included (all)
	inc(algoProgpow, perAlgoTotal[string(algoProgpow)], &wsCtr.candidateTotal, &wsCtr.candidateProgpow, &wsCtr.candidateKawpow, &wsCtr.candidateSHA, &wsCtr.candidateScrypt)
	inc(algoKawpow, perAlgoTotal[string(algoKawpow)], &wsCtr.candidateTotal, &wsCtr.candidateProgpow, &wsCtr.candidateKawpow, &wsCtr.candidateSHA, &wsCtr.candidateScrypt)
	inc(algoSHA, perAlgoTotal[string(algoSHA)], &wsCtr.candidateTotal, &wsCtr.candidateProgpow, &wsCtr.candidateKawpow, &wsCtr.candidateSHA, &wsCtr.candidateScrypt)
	inc(algoScrypt, perAlgoTotal[string(algoScrypt)], &wsCtr.candidateTotal, &wsCtr.candidateProgpow, &wsCtr.candidateKawpow, &wsCtr.candidateSHA, &wsCtr.candidateScrypt)

	// Update global totals for included (mine)
	inc(algoProgpow, perAlgoMine[string(algoProgpow)], &wsCtr.candidateMineTotal, &wsCtr.candidateMineProgpow, &wsCtr.candidateMineKawpow, &wsCtr.candidateMineSHA, &wsCtr.candidateMineScrypt)
	inc(algoKawpow, perAlgoMine[string(algoKawpow)], &wsCtr.candidateMineTotal, &wsCtr.candidateMineProgpow, &wsCtr.candidateMineKawpow, &wsCtr.candidateMineSHA, &wsCtr.candidateMineScrypt)
	inc(algoSHA, perAlgoMine[string(algoSHA)], &wsCtr.candidateMineTotal, &wsCtr.candidateMineProgpow, &wsCtr.candidateMineKawpow, &wsCtr.candidateMineSHA, &wsCtr.candidateMineScrypt)
	inc(algoScrypt, perAlgoMine[string(algoScrypt)], &wsCtr.candidateMineTotal, &wsCtr.candidateMineProgpow, &wsCtr.candidateMineKawpow, &wsCtr.candidateMineSHA, &wsCtr.candidateMineScrypt)

	log.Global.WithFields(log.Fields{
		"type":         "workshare.block_inclusion",
		"block":        blockHash,
		"perAlgoTotal": perAlgoTotal,
		"perAlgoMine":  perAlgoMine,
		"totals":       snapshot(),
	}).Info("Workshare inclusion summary for block")
}

// snapshot returns a map snapshot of the current aggregate counters for structured logs.
func snapshot() map[string]any {
	ensureLRUs()
	return map[string]any{
		"mined": map[string]uint64{
			"total":   wsCtr.minedTotal.Load(),
			"progpow": wsCtr.minedProgpow.Load(),
			"kawpow":  wsCtr.minedKawpow.Load(),
			"sha":     wsCtr.minedSHA.Load(),
			"scrypt":  wsCtr.minedScrypt.Load(),
		},
		"received": map[string]uint64{
			"total":   wsCtr.receivedTotal.Load(),
			"progpow": wsCtr.receivedProgpow.Load(),
			"kawpow":  wsCtr.receivedKawpow.Load(),
			"sha":     wsCtr.receivedSHA.Load(),
			"scrypt":  wsCtr.receivedScrypt.Load(),
		},
		"candidate": map[string]uint64{
			"total":   wsCtr.candidateTotal.Load(),
			"progpow": wsCtr.candidateProgpow.Load(),
			"kawpow":  wsCtr.candidateKawpow.Load(),
			"sha":     wsCtr.candidateSHA.Load(),
			"scrypt":  wsCtr.candidateScrypt.Load(),
		},
		"candidateMine": map[string]uint64{
			"total":   wsCtr.candidateMineTotal.Load(),
			"progpow": wsCtr.candidateMineProgpow.Load(),
			"kawpow":  wsCtr.candidateMineKawpow.Load(),
			"sha":     wsCtr.candidateMineSHA.Load(),
			"scrypt":  wsCtr.candidateMineScrypt.Load(),
		},
		"lru": map[string]int{
			"mined":     minedLRU.Len(),
			"received":  receivedLRU.Len(),
			"candidate": candidateLRU.Len(),
		},
	}
}

// BuildWorkshareLRUDump builds a structured dump of the current WS LRUs.
// limit caps the number of entries per list returned.
func BuildWorkshareLRUDump(limit int) map[string]interface{} {
	ensureLRUs()

	type entry struct {
		Hash   string
		Algo   string
		AgeSec int64
		Stage  string
	}
	now := time.Now()

	makeEntry := func(h common.Hash, rec *WorkshareRecord, stage string) entry {
		algo := string(algoFromHeader(rec.Header))
		age := int64(0)
		if !rec.FirstSeen.IsZero() {
			age = int64(now.Sub(rec.FirstSeen).Seconds())
		}
		return entry{
			Hash:   h.Hex(),
			Algo:   algo,
			AgeSec: age,
			Stage:  stage,
		}
	}
	marshalHeaderJSON := func(hdr *types.WorkObjectHeader) string {
		if hdr == nil {
			return ""
		}
		js, err := json.Marshal(hdr.RPCMarshalWorkObjectHeader("v2"))
		if err != nil {
			return ""
		}
		return string(js)
	}

	minedNotReceived := make([]entry, 0)
	minedNotReceivedJSON := make([]string, 0)
	for _, h := range minedLRU.Keys() {
		if _, ok := receivedLRU.Get(h); ok {
			continue
		}
		if rec, ok := minedLRU.Get(h); ok && rec != nil {
			minedNotReceived = append(minedNotReceived, makeEntry(h, rec, "mined"))
			minedNotReceivedJSON = append(minedNotReceivedJSON, marshalHeaderJSON(rec.Header))
		}
	}

	receivedNotCandidate := make([]entry, 0)
	receivedNotCandidateJSON := make([]string, 0)
	for _, h := range receivedLRU.Keys() {
		if _, ok := candidateLRU.Get(h); ok {
			continue
		}
		if rec, ok := receivedLRU.Get(h); ok && rec != nil {
			receivedNotCandidate = append(receivedNotCandidate, makeEntry(h, rec, "received"))
			receivedNotCandidateJSON = append(receivedNotCandidateJSON, marshalHeaderJSON(rec.Header))
		}
	}

	candidate := make([]entry, 0)
	candidateJSON := make([]string, 0)
	for _, h := range candidateLRU.Keys() {
		if rec, ok := candidateLRU.Get(h); ok && rec != nil {
			candidate = append(candidate, makeEntry(h, rec, "candidate"))
			candidateJSON = append(candidateJSON, marshalHeaderJSON(rec.Header))
		}
	}

	return map[string]interface{}{
		"sizes":                       map[string]int{"mined": minedLRU.Len(), "received": receivedLRU.Len(), "candidate": candidateLRU.Len()},
		"mined_not_received_cnt":      len(minedNotReceived),
		"received_not_candidate_cnt":  len(receivedNotCandidate),
		"candidate_cnt":               len(candidate),
		"mined_not_received":          limitEntries(minedNotReceived, limit),
		"received_not_candidate":      limitEntries(receivedNotCandidate, limit),
		"candidate":                   limitEntries(candidate, limit),
		"mined_not_received_json":     limitEntries(minedNotReceivedJSON, limit),
		"received_not_candidate_json": limitEntries(receivedNotCandidateJSON, limit),
		"candidate_json":              limitEntries(candidateJSON, limit),
	}
}

// DumpWorkshareLRUs logs a snapshot of the workshare LRUs (kept for convenience).
func DumpWorkshareLRUs() {
	d := BuildWorkshareLRUDump(100)
	log.Global.WithFields(log.Fields{
		"type": "workshare.lru_dump",
		// Flatten d map into fields
		"sizes":                       d["sizes"],
		"mined_not_received_cnt":      d["mined_not_received_cnt"],
		"received_not_candidate_cnt":  d["received_not_candidate_cnt"],
		"candidate_cnt":               d["candidate_cnt"],
		"mined_not_received_json":     d["mined_not_received_json"],
		"received_not_candidate_json": d["received_not_candidate_json"],
		"candidate_json":              d["candidate_json"],
	}).Info("Workshare LRU dump")
}
