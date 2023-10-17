package node

import (
	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/spf13/viper"
)

// Returns the enabled NAT related options for the libp2p node
func getNATOptions() []libp2p.Option {
	// list of NAT related options to instantiate the libp2p node
	nodeOptions := []libp2p.Option{}
	// get parameters from config, flags or environment variables
	if !viper.GetBool("nat") {
		log.Debugf("No NAT related options used to create node")
		return nodeOptions
	}
	// include option for NAT port mapping (by opening a port in network's firewall using UPnP.)
	log.Debugf("Enabling NAT port mapping...")
	nodeOptions = append(nodeOptions, libp2p.NATPortMap())

	// include option to provide NAT service to peers for determining their reachability status
	log.Debugf("Enabling NAT service...")
	nodeOptions = append(nodeOptions, libp2p.EnableNATService())

	// include option to enable auto relay with static relays addresses
	staticRelays := viper.GetStringSlice("static-relays")
	if len(staticRelays) > 0 {
		log.Debugf("Enabling auto relay with %d static relays addresses...", len(staticRelays))
		staticRelaysAddr := make([]peer.AddrInfo, len(staticRelays))
		for i, addr := range staticRelays {
			pi, err := peer.AddrInfoFromString(addr)
			if err != nil {
				log.Errorf("error parsing static relay address: %s", err)
			}
			staticRelaysAddr[i] = *pi
		}
		nodeOptions = append(nodeOptions, libp2p.EnableAutoRelayWithStaticRelays(staticRelaysAddr))
	} else {
		log.Debugf("Bypassing auto relay with static relays. No static relays provided")
	}

	// include option to enable circuit relay.
	// This option enables the node to act as a relay for other peers.
	log.Debugf("Enabling circuit relay...")
	nodeOptions = append(nodeOptions, libp2p.EnableRelay())

	// include option to enable hole punching: this option enables NAT traversal
	// by enabling NATT'd peers to both initiate and respond to hole punching attempts
	// to create direct/NAT-traversed connections with other peers.
	// see: https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#EnableHolePunching
	log.Debugf("Enabling hole punching with default options...")
	nodeOptions = append(nodeOptions, libp2p.EnableHolePunching())

	return nodeOptions
}
