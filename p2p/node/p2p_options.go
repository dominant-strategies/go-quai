package node

import (
	"github.com/dominant-strategies/go-quai/config"
	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"

	"github.com/spf13/viper"
)

// loadP2PNodeOptions loads the options for the libp2p node using viper
// as the configuration main aggregator.
func loadP2PNodeOptions() []libp2p.Option {
	log.Info("Loading P2P Node Options...")
	nodeOptions := []libp2p.Option{}
	// Check for NAT Service
	if viper.GetBool(config.NAT_SERVICE) {
		nodeOptions = append(nodeOptions, libp2p.EnableNATService())
		log.Info("NAT Service enabled...")
	}

	// Check for NAT PortMap
	if viper.GetBool(config.NAT_PORTMAP) {
		nodeOptions = append(nodeOptions, libp2p.NATPortMap())
		log.Info("NAT PortMap enabled...")
	}

	// Check for AutoRelay with Static Relays
	if viper.GetBool(config.AUTO_RELAY_STATIC) {
		// Check for Static Relays in config/static_relays.go
		stringAddrs := config.EstaticRelays
		if len(stringAddrs) == 0 {
			log.Error("No static relay addresses found. Skipping...")
		} else {
			log.Debug("Found static relay addresses:", stringAddrs)
			staticRelays, err := convertToPeerAddrInfo(stringAddrs)
			if err != nil {
				log.Error("Failed to convert static relay addresses:", err)
			} else {
				nodeOptions = append(nodeOptions, libp2p.EnableAutoRelayWithStaticRelays(staticRelays))
				log.Info("AutoRelay with Static Relays enabled...")
			}
		}
	}

	// Check for Hole Punching
	if viper.GetBool(config.HOLE_PUNCHING) {
		// ensure that Circuit Relay is enabled
		if !viper.GetBool(config.RELAY) {
			log.Debug("Circuit Relay must be enabled for Hole Punching to work. Enabling Circuit Relay...")
			viper.Set(config.RELAY, true)
		}
		// TODO: should we add options for Hole Punching?
		nodeOptions = append(nodeOptions, libp2p.EnableHolePunching())
		log.Info("Hole Punching enabled...")
	}

	// Relay (Circuit Relay) is enabled by default in libp2p
	// If Hole Punching is enabled, Circuit Relay is already enabled above for clarity.
	// We only need to disable it if the user explicitly sets the flag to false.
	if !viper.GetBool(config.RELAY) && !viper.GetBool(config.HOLE_PUNCHING) {
		nodeOptions = append(nodeOptions, libp2p.DisableRelay())
		log.Info("Circuit Relay disabled...")
	} else {
		log.Info("Circuit Relay enabled...")
	}

	return nodeOptions

}

// Helper function to convert slice of string multiaddresses to peer.AddrInfo
func convertToPeerAddrInfo(addrs []string) ([]peer.AddrInfo, error) {
	var addrInfos []peer.AddrInfo
	for _, a := range addrs {
		multiaddr, err := multiaddr.NewMultiaddr(a)
		if err != nil {
			return nil, err
		}
		addrInfo, err := peer.AddrInfoFromP2pAddr(multiaddr)
		if err != nil {
			return nil, err
		}
		addrInfos = append(addrInfos, *addrInfo)
	}
	return addrInfos, nil
}
