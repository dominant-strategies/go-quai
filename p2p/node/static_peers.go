package node

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/log"
)

const (
	c_staticPeerRetryMin       = 2 * time.Second
	c_staticPeerRetryMax       = 1 * time.Minute
	c_staticPeerCheckInterval  = 30 * time.Second
	c_staticPeerConnectTimeout = 10 * time.Second
)

func (p *P2PNode) startStaticPeerConnector() {
	if len(p.staticPeers) == 0 {
		return
	}

	// A static peer is treated as "sticky":
	// - we continuously try to keep a connection open
	// - we protect it from connection manager pruning
	// - we proactively open a request/response stream
	for _, info := range p.staticPeers {
		info := info
		if info.ID == "" || info.ID == p.peerManager.GetSelfID() {
			continue
		}
		go p.maintainStaticPeerConnection(info)
	}
}

func (p *P2PNode) maintainStaticPeerConnection(info peer.AddrInfo) {
	// Exponential backoff: don't spam dials if the peer is down/unreachable.
	retry := c_staticPeerRetryMin
	for {
		if p.ctx.Err() != nil {
			return
		}

		if p.peerManager.GetHost().Network().Connectedness(info.ID) != network.Connected {
			connectCtx, cancel := context.WithTimeout(p.ctx, c_staticPeerConnectTimeout)
			err := p.peerManager.GetHost().Connect(connectCtx, info)
			cancel()
			if err != nil {
				log.Global.WithFields(log.Fields{
					"peer": info.ID.String(),
					"err":  err,
				}).Warn("Failed to connect to static peer")

				select {
				case <-time.After(retry):
				case <-p.ctx.Done():
					return
				}
				retry *= 2
				if retry > c_staticPeerRetryMax {
					retry = c_staticPeerRetryMax
				}
				continue
			}

			retry = c_staticPeerRetryMin
			// Keep this connection around even under normal peer churn/pressure.
			p.ProtectPeer(info.ID)
			// Eagerly open the request/response stream; this avoids "first request
			// loses" behavior in environments with low connectivity or high latency.
			if err := p.peerManager.OpenStream(info.ID); err != nil {
				log.Global.WithFields(log.Fields{
					"peer": info.ID.String(),
					"err":  err,
				}).Debug("Connected to static peer but failed to open stream")
			}

			log.Global.WithFields(log.Fields{
				"peer":  info.ID.String(),
				"addrs": info.Addrs,
			}).Info("Connected to static peer")
		}

		select {
		case <-time.After(c_staticPeerCheckInterval):
		case <-p.ctx.Done():
			return
		}
	}
}
