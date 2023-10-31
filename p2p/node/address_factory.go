package node

import (
	"fmt"
	"os"
	"strings"

	"github.com/dominant-strategies/go-quai/log"

	multiaddr "github.com/multiformats/go-multiaddr"
)

var addressReplaced = false

// returns a function that replaces the internal IP (i.e. Docker IP) with the host IP
func makeAddrsFactory(hostIP string) func([]multiaddr.Multiaddr) []multiaddr.Multiaddr {
	return func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		if addressReplaced {
			return addrs
		}

		// Append a new address with the host IP and the same port as the Docker IP
		for _, addr := range addrs {
			if !isLoopbackAddr(addr) {
				parts := strings.Split(addr.String(), "/")
				if len(parts) > 1 && parts[1] == "ip4" && parts[2] != hostIP {
					port := parts[4]
					newAddrStr := fmt.Sprintf("/ip4/%s/tcp/%s", hostIP, port)
					newAddr, err := multiaddr.NewMultiaddr(newAddrStr)
					if err == nil {
						addrs = append(addrs, newAddr)
						log.Debugf("added address: %s", newAddr.String())
					} else {
						log.Errorf("error creating new multiaddr from addr %s, error: %s", newAddrStr, err)
					}
					break
				}
			}
		}
		addressReplaced = true
		return addrs
	}
}

func isLoopbackAddr(addr multiaddr.Multiaddr) bool {
	return strings.Contains(addr.String(), "127.0.0.1") || strings.Contains(addr.String(), "localhost")
}

// reads the host IP from a file
func readHostIPFromFile(filename string) (string, error) {
	log.Debugf("reading host IP from file: %s", filename)
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
