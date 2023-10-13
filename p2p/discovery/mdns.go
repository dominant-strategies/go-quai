package discovery

import (
	"context"
	"time"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

const (
	// DiscoveryServiceTag is used in our mDNS advertisements to discover other peers.
	DiscoveryServiceTag = "quai"
	// DiscoveryInterval is how often we re-publish our mDNS records.
	DiscoveryInterval = time.Hour
)

type mDNSNotifee struct {
	h   host.Host
	ctx context.Context
}

func (d *mDNSNotifee) HandlePeerFound(pi peer.AddrInfo) {
	log.Debugf("discovered new peer %s", pi.ID.Pretty())
	err := d.h.Connect(d.ctx, pi)
	if err != nil {
		log.Errorf("error connecting to peer: %s", err)
	}
}

type MdnsService struct {
	service mdns.Service
}

func (s *MdnsService) Start() error {
	return s.service.Start()
}

func (s *MdnsService) Stop() error {
	return s.service.Close()
}

func NewmDNSDiscovery(ctx context.Context, h host.Host) Discovery {
	log.Debugf("creating mDNS discovery service for host %s", h.ID().Pretty())
	notifee := &mDNSNotifee{
		h:   h,
		ctx: ctx,
	}

	mDNS := &MdnsService{}
	mDNS.service = mdns.NewMdnsService(h, DiscoveryServiceTag, notifee)

	return mDNS
}
