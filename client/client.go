package client

import (
	"context"
	"net/http"

	p2pnode "github.com/dominant-strategies/go-quai/p2p/node"
)

type P2PClient struct {
	node       *p2pnode.P2PNode
	httpServer *http.Server
	ctx        context.Context
}

func NewClient(ctx context.Context, node *p2pnode.P2PNode) *P2PClient {
	client := &P2PClient{
		node: node,
		ctx:  ctx,
	}

	client.node.SetStreamHandler(myProtocol, client.handleStream)
	return client
}
