package main

import (
	"compress/gzip"
	"io"
	"log"
	"os"
	"strings"

	"github.com/spruce-solutions/go-quai/consensus/blake3"
	"github.com/spruce-solutions/go-quai/core"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/params"
)

func main() {
	var (
		testdb  = rawdb.NewMemoryDatabase()
		genesis = core.RopstenPrimeGenesisBlock().MustCommit(testdb)
		fn      = "test_knot.rlp"
	)

	blake3Config := blake3.Config{
		MiningThreads: 0,
		NotifyFull:    true,
	}

	blake3Engine, err := blake3.New(blake3Config, nil, false)
	if nil != err {
		log.Fatal("Failed to create Blake3 engine: ", err)
	}

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
