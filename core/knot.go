package core

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/rlp"
)

func ReadKnot(chainfile string) []*types.Block {
	// Load chain.rlp.
	fmt.Println("Loading ReadKnot")
	fh, err := os.Open(chainfile)
	if err != nil {
		fmt.Println("OPEN ERR 1", err)
		return nil
	}
	defer fh.Close()
	var reader io.Reader = fh
	if strings.HasSuffix(chainfile, ".gz") {
		if reader, err = gzip.NewReader(reader); err != nil {
			fmt.Println("READER ERR 1", err)
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
			fmt.Println("DECODE ERR 1", err)
			return nil
		}
		if b.NumberU64() != uint64(i+1) {
			fmt.Println("DECODE ERR 2", err)
			return nil
		}
		blocks = append(blocks, &b)
	}
	for _, block := range blocks {
		fmt.Println(block)
	}
	return blocks
}
