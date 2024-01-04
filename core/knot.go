package core

import (
	"compress/gzip"
	"io"
	"os"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
)

func ReadKnot(chainfile string) []*types.Block {
	nodeCtx := common.NodeLocation.Context()
	// Load chain.rlp.
	fh, err := os.Open(chainfile)
	if err != nil {
		log.Error("Error in ReadKnot", "Err", err)
		return nil
	}
	defer fh.Close()
	var reader io.Reader = fh
	if strings.HasSuffix(chainfile, ".gz") {
		if reader, err = gzip.NewReader(reader); err != nil {
			log.Error("Error in ReadKnot", "Err", err)
			return nil
		}
	}
	stream := rlp.NewStream(reader, 0)
	var blocks = make([]*types.Block, 0)
	for i := 0; ; i++ {
		b := &types.Block{}
		if err := stream.Decode(b); err == io.EOF {
			break
		} else if err != nil {
			log.Error("Error in ReadKnot", "Err", err)
			return nil
		}
		h := b.Header()
		// If we have a subordinate, we need to rebuild the block with the correct
		// manifest of subordinate blocks
		if nodeCtx < common.ZONE_CTX {
			subManifest := types.BlockManifest{h.ParentHash(nodeCtx + 1)}
			b = types.NewBlock(h, nil, nil, nil, subManifest, nil, trie.NewStackTrie(nil))
		}
		blocks = append(blocks, b)
	}
	return blocks
}
