package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/nipopow"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/trie"
)

const defaultTailLengths = "16,64,256,1024"

type validationConfig struct {
	M           uint64
	Ranges      []nipopow.ValidationRange
	TailLengths []uint64
	Limits      nipopow.BuildLimits
	Timeout     time.Duration
}

type cliConfig struct {
	DBPath      string
	AncientPath string
	DBEngine    string
	OutPath     string
	Validation  validationConfig
}

type validationOutput struct {
	StartedAtUTC        string                    `json:"startedAtUtc"`
	FinishedAtUTC       string                    `json:"finishedAtUtc"`
	DBPath              string                    `json:"dbPath,omitempty"`
	AncientPath         string                    `json:"ancientPath,omitempty"`
	ReadOnly            bool                      `json:"readOnly"`
	Location            string                    `json:"location"`
	HeadHash            common.Hash               `json:"headHash"`
	HeadNumber          uint64                    `json:"headNumber"`
	GenesisHashes       []common.Hash             `json:"genesisHashes"`
	SelectedRanges      []nipopow.ValidationRange `json:"selectedRanges"`
	M                   uint64                    `json:"m"`
	Limits              nipopow.BuildLimits       `json:"limits"`
	OpenElapsedMS       int64                     `json:"openElapsedMs,omitempty"`
	ValidationElapsedMS int64                     `json:"validationElapsedMs"`
	Report              *nipopow.ValidationReport `json:"report,omitempty"`
	OK                  bool                      `json:"ok"`
	Error               string                    `json:"error,omitempty"`
}

type rawPrimeSource struct {
	db      ethdb.Database
	genesis map[common.Hash]struct{}
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	log.Global.SetOutput(io.Discard)
	log.Global.SetLevel(logrus.ErrorLevel)

	cfg, err := parseCLI(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	out := validationOutput{
		StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		DBPath:       cfg.DBPath,
		AncientPath:  cfg.AncientPath,
		ReadOnly:     true,
		Location:     "prime",
		M:            cfg.Validation.M,
		Limits:       cfg.Validation.Limits,
	}

	openStarted := time.Now()
	db, err := rawdb.Open(rawdb.OpenOptions{
		Type:              cfg.DBEngine,
		Directory:         cfg.DBPath,
		AncientsDirectory: cfg.AncientPath,
		Namespace:         "nipopow/prime-validate/",
		Cache:             64,
		Handles:           64,
		ReadOnly:          true,
	}, common.PRIME_CTX, log.Global, common.Location{})
	out.OpenElapsedMS = time.Since(openStarted).Milliseconds()
	if err != nil {
		out.Error = fmt.Sprintf("open readonly db: %v", err)
		out.FinishedAtUTC = time.Now().UTC().Format(time.RFC3339Nano)
		_ = writeReport(stdout, cfg.OutPath, out)
		return 1
	}
	defer db.Close()

	report, err := runValidation(context.Background(), db, cfg.Validation)
	out.FinishedAtUTC = time.Now().UTC().Format(time.RFC3339Nano)
	out.HeadHash = report.HeadHash
	out.HeadNumber = report.HeadNumber
	out.GenesisHashes = report.GenesisHashes
	out.SelectedRanges = report.SelectedRanges
	out.M = report.M
	out.Limits = report.Limits
	out.ValidationElapsedMS = report.ValidationElapsedMS
	out.Report = report.Report
	out.OK = report.OK
	if err != nil {
		out.Error = err.Error()
		out.OK = false
	} else if !out.OK {
		out.Error = "one or more validation ranges failed"
	}
	if err := writeReport(stdout, cfg.OutPath, out); err != nil {
		fmt.Fprintf(stderr, "error writing report: %v\n", err)
		return 1
	}
	if !out.OK {
		return 1
	}
	return 0
}

func parseCLI(args []string, stderr io.Writer) (cliConfig, error) {
	var rangesSpec string
	var lengthsSpec string
	cfg := cliConfig{}
	fs := flag.NewFlagSet("nipopow-prime-validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.DBPath, "db", "", "Prime chaindata path to open read-only")
	fs.StringVar(&cfg.AncientPath, "ancient", "", "Prime ancient freezer path; defaults to <db>/ancient when it exists")
	fs.StringVar(&cfg.DBEngine, "db.engine", "leveldb", "Database engine: leveldb or pebble")
	fs.StringVar(&cfg.OutPath, "out", "", "Optional JSON report output path; stdout is used when empty")
	fs.Uint64Var(&cfg.Validation.M, "m", 16, "NiPoPoW suffix length")
	fs.StringVar(&lengthsSpec, "lengths", defaultTailLengths, "Comma-separated tail lengths used when --ranges is empty")
	fs.StringVar(&rangesSpec, "ranges", "", "Explicit ranges as name=anchor:tip,name2=anchor:tip; overrides --lengths")
	fs.Uint64Var(&cfg.Validation.Limits.MaxChainLength, "max-chain", nipopow.DefaultMaxProofChainLength, "Maximum canonical chain walk length")
	fs.Uint64Var(&cfg.Validation.Limits.MaxProofHeaders, "max-headers", nipopow.DefaultMaxProofHeaders, "Maximum compressed proof header count")
	fs.Uint64Var(&cfg.Validation.Limits.MaxM, "max-m", nipopow.DefaultMaxProofM, "Maximum allowed m")
	fs.DurationVar(&cfg.Validation.Timeout, "timeout", 2*time.Minute, "Validation timeout")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.DBPath == "" {
		return cfg, errors.New("missing required --db")
	}
	if cfg.AncientPath == "" {
		candidate := filepath.Join(cfg.DBPath, "ancient")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			cfg.AncientPath = candidate
		}
	}
	if rangesSpec != "" {
		ranges, err := parseRanges(rangesSpec)
		if err != nil {
			return cfg, err
		}
		cfg.Validation.Ranges = ranges
		return cfg, nil
	}
	lengths, err := parseLengths(lengthsSpec)
	if err != nil {
		return cfg, err
	}
	cfg.Validation.TailLengths = lengths
	return cfg, nil
}

func writeReport(stdout io.Writer, outPath string, report validationOutput) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if outPath == "" {
		_, err = stdout.Write(payload)
		return err
	}
	return os.WriteFile(outPath, payload, 0o644)
}

func runValidation(ctx context.Context, db ethdb.Database, cfg validationConfig) (validationOutput, error) {
	out := validationOutput{
		StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		ReadOnly:     true,
		Location:     "prime",
		M:            cfg.M,
		Limits:       cfg.Limits,
	}
	defer func() {
		out.FinishedAtUTC = time.Now().UTC().Format(time.RFC3339Nano)
	}()
	if db == nil {
		return out, errors.New("nil database")
	}
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	source := newRawPrimeSource(db)
	out.GenesisHashes = source.genesisHashes()

	head := rawdb.ReadHeadBlockHash(db)
	if head == (common.Hash{}) {
		head = rawdb.ReadHeadHeaderHash(db)
	}
	if head == (common.Hash{}) {
		return out, errors.New("missing prime head hash")
	}
	headNumber := rawdb.ReadHeaderNumber(db, head)
	if headNumber == nil {
		return out, fmt.Errorf("missing prime head number for %s", head.Hex())
	}
	out.HeadHash = head
	out.HeadNumber = *headNumber

	ranges := cfg.Ranges
	if len(ranges) == 0 {
		maxChain := cfg.Limits.MaxChainLength
		if maxChain == 0 {
			maxChain = nipopow.DefaultMaxProofChainLength
		}
		ranges = selectTailRanges(*headNumber, cfg.M, maxChain, cfg.TailLengths)
	}
	out.SelectedRanges = ranges
	if len(ranges) == 0 {
		return out, fmt.Errorf("no validation ranges selected for head=%d m=%d", *headNumber, cfg.M)
	}

	started := time.Now()
	report, err := nipopow.ValidatePrimeProofRanges(ctx, source, nipopow.ValidationOptions{
		M:      cfg.M,
		Ranges: ranges,
		Limits: cfg.Limits,
	})
	out.ValidationElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		return out, err
	}
	out.Report = report
	out.OK = !report.Failed()
	return out, nil
}

func parseLengths(spec string) ([]uint64, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	lengths := make([]uint64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid length %q: %w", part, err)
		}
		lengths = append(lengths, value)
	}
	return lengths, nil
}

func parseRanges(spec string) ([]nipopow.ValidationRange, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	ranges := make([]nipopow.ValidationRange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := ""
		rangeSpec := part
		if before, after, ok := strings.Cut(part, "="); ok {
			name = strings.TrimSpace(before)
			rangeSpec = strings.TrimSpace(after)
			if name == "" {
				return nil, fmt.Errorf("invalid range %q: empty name", part)
			}
		}
		anchorSpec, tipSpec, ok := strings.Cut(rangeSpec, ":")
		if !ok {
			return nil, fmt.Errorf("invalid range %q: expected anchor:tip", part)
		}
		anchor, err := strconv.ParseUint(strings.TrimSpace(anchorSpec), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid range %q anchor: %w", part, err)
		}
		tip, err := strconv.ParseUint(strings.TrimSpace(tipSpec), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid range %q tip: %w", part, err)
		}
		if name == "" {
			name = fmt.Sprintf("%d-%d", anchor, tip)
		}
		ranges = append(ranges, nipopow.ValidationRange{Name: name, AnchorNumber: anchor, TipNumber: tip})
	}
	return ranges, nil
}

func selectTailRanges(headNumber uint64, m uint64, maxChain uint64, lengths []uint64) []nipopow.ValidationRange {
	ranges := make([]nipopow.ValidationRange, 0, len(lengths))
	for _, length := range lengths {
		if length == 0 || length < m || length > maxChain || headNumber+1 < length {
			continue
		}
		ranges = append(ranges, nipopow.ValidationRange{
			Name:         fmt.Sprintf("tail-%d", length),
			AnchorNumber: headNumber - length + 1,
			TipNumber:    headNumber,
		})
	}
	return ranges
}

func newRawPrimeSource(db ethdb.Database) *rawPrimeSource {
	genesis := make(map[common.Hash]struct{})
	for _, hash := range rawdb.ReadGenesisHashes(db) {
		if hash != (common.Hash{}) {
			genesis[hash] = struct{}{}
		}
	}
	if hash := rawdb.ReadCanonicalHash(db, 0); hash != (common.Hash{}) {
		genesis[hash] = struct{}{}
	}
	return &rawPrimeSource{db: db, genesis: genesis}
}

func (s *rawPrimeSource) GetCanonicalHash(number uint64) common.Hash {
	return rawdb.ReadCanonicalHash(s.db, number)
}

func (s *rawPrimeSource) ProofHeader(hash common.Hash) (*types.WorkObject, error) {
	number := rawdb.ReadHeaderNumber(s.db, hash)
	if number == nil {
		return nil, fmt.Errorf("header number not found for %s", hash.Hex())
	}
	header := rawdb.ReadHeader(s.db, *number, hash)
	if header == nil {
		return nil, fmt.Errorf("header not found for number=%d hash=%s", *number, hash.Hex())
	}
	if header.Hash() != hash {
		return nil, fmt.Errorf("header hash mismatch number=%d expected=%s got=%s", *number, hash.Hex(), header.Hash().Hex())
	}

	proofHeader := types.CopyWorkObject(header)
	interlinkSource := proofHeader.ParentHash(common.PRIME_CTX)
	if s.isGenesis(hash) {
		interlinkSource = hash
	}
	interlinks := rawdb.ReadInterlinkHashes(s.db, interlinkSource)
	if interlinks == nil {
		if !s.isGenesis(hash) {
			return nil, fmt.Errorf("interlink hashes not found for source=%s header=%s number=%d", interlinkSource.Hex(), hash.Hex(), *number)
		}
		interlinks = common.Hashes{}
	}
	proofHeader.Body().SetInterlinkHashes(interlinks)

	expected := types.DeriveSha(interlinks, trie.NewStackTrie(nil))
	if proofHeader.InterlinkRootHash() != expected {
		return nil, fmt.Errorf("interlink root mismatch for header=%s number=%d source=%s expected=%s got=%s interlinks=%d", hash.Hex(), *number, interlinkSource.Hex(), expected.Hex(), proofHeader.InterlinkRootHash().Hex(), len(interlinks))
	}
	return proofHeader, nil
}

func (s *rawPrimeSource) isGenesis(hash common.Hash) bool {
	_, ok := s.genesis[hash]
	return ok
}

func (s *rawPrimeSource) genesisHashes() []common.Hash {
	hashes := make([]common.Hash, 0, len(s.genesis))
	for hash := range s.genesis {
		hashes = append(hashes, hash)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].Hex() < hashes[j].Hex() })
	return hashes
}
