package main

import (
	"compress/gzip"
	"io"
	"log"
	"os"
	"strings"

	"github.com/dominant-strategies/go-quai/consensus/blake3pow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/params"
)

func main() {
	var (
		testdb  = rawdb.NewMemoryDatabase()
		genesis = core.DefaultRopstenGenesisBlock().MustCommit(testdb)
		fn      = "ropsten_knot.rlp"
	)

	blake3Config := blake3pow.Config{
		NotifyFull: true,
	}

	blake3Engine := blake3pow.New(blake3Config, nil, false)

	blocks, _ := core.GenerateKnot(params.TestChainConfig, genesis, blake3Engine, testdb, 9, nil)

	// Open the file handle and potentially wrap with a gzip stream
	fh, err := os.OpenFile(fn, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
	if err != nil {
		log.Panic("error opening file")
	}
	defer fh.Close()

	var writer io.Writer = fh
	if strings.HasSuffix(fn, ".gz") {
		writer = gzip.NewWriter(writer)
		defer writer.(*gzip.Writer).Close()
	}

	for _, block := range blocks {
		if err := block.EncodeRLP(writer); err != nil {
			log.Panic("error writing")
		}
	}

	log.Println("Exported blockchain", "file", fn)
}
