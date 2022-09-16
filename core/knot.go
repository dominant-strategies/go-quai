package core

import (
	"compress/gzip"
	"io"
	"os"
	"strings"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/rlp"
)

func ReadKnot(chainfile string) []*types.Block {
	// Load chain.rlp.
	fh, err := os.Open(chainfile)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var reader io.Reader = fh
	if strings.HasSuffix(chainfile, ".gz") {
		if reader, err = gzip.NewReader(reader); err != nil {
			return nil
		}
	}
	stream := rlp.NewStream(reader, 0)
	var blocks = make([]*types.Block, 1)
	for i := 0; ; i++ {
		var b types.Block
		if err := stream.Decode(&b); err == io.EOF {
			break
		} else if err != nil {
			return nil
		}
		if b.NumberU64() != uint64(i+1) {
			return nil
		}
		blocks = append(blocks, &b)
	}
	return blocks
}
