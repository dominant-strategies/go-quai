package client

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/dominant-strategies/go-quai/log"

	// log "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"
)

func (c *P2PClient) StartServer(port string) error {
	// start http server
	mux := c.newServer()
	c.httpServer = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}
	log.Infof("http server listening on port %s", port)
	return c.httpServer.ListenAndServe()
}

func (c *P2PClient) StopServer() {
	err := c.httpServer.Shutdown(context.Background())
	if err != nil {
		log.Fatalf("error stopping http server: %s", err)
	}
}

// handler for the /dhtpeers endpoint
func (c *P2PClient) dhtPeersHandler(w http.ResponseWriter, r *http.Request) {
	// get the list of peers from the dht
	peers := c.node.GetPeers()
	log.Debugf("peers on dht: %d", len(peers))
	// write the list of peers to the response
	for _, peer := range peers {
		w.Write([]byte(peer.Pretty() + "\n"))
	}
}

// handler for the /nodepeers endpoint
func (c *P2PClient) nodePeersHandler(w http.ResponseWriter, r *http.Request) {
	// get the list of peers from the host
	peers := c.node.Peerstore().Peers()
	// write the list of peers to the response
	log.Debugf("peers on peerStore: %d", len(peers))
	for _, peer := range peers {
		w.Write([]byte(peer.Pretty() + "\n"))
	}
}

// handler for the /connect/{multiaddr} endpoint
func (c *P2PClient) connectHandler(w http.ResponseWriter, r *http.Request) {
	addr := strings.TrimPrefix(r.URL.Path, "/connect")
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		log.Errorf("Failed to parse multiaddress: %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		log.Errorf("Failed to parse multiaddress: %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Discover the Mac node's addresses using DHT
	discoveredAddrs, err := c.node.FindPeer(c.ctx, addrInfo.ID)
	if err != nil {
		log.Errorf("Failed to discover peer: %v, error: %s", addrInfo.ID, err)
		http.Error(w, "Failed to discover peer: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Info("Discovered addresses:", discoveredAddrs)

	ctx := context.Background()
	if err := c.node.Connect(ctx, *addrInfo); err != nil {
		log.Errorf("Failed to connect to peer: %v, %s", addrInfo.ID.Pretty(), err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte("Connected successfully to peer " + addrInfo.ID.Pretty()))
}

// handler for the /discover/{peerID} endpoint
func (c *P2PClient) discoverHandler(w http.ResponseWriter, r *http.Request) {
	peerIDStr := strings.TrimPrefix(r.URL.Path, "/discover/")
	peerID, err := peer.Decode(peerIDStr)
	if err != nil {
		log.Errorf("Failed to decode peer ID: %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Debugf("Received request to discover Peer ID: %s", peerID.String())

	// Discover the peer's addresses using DHT
	discoveredAddrsInfo, err := c.node.FindPeer(c.ctx, peerID)
	if err != nil {
		log.Errorf("Failed to discover peer: %v, error: %s", peerID, err)
		http.Error(w, "Failed to discover peer: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Info("Discovered addresses:", discoveredAddrsInfo)

	if err := c.node.Connect(c.ctx, discoveredAddrsInfo); err != nil {
		log.Errorf("Failed to connect to peer: %v, %s", discoveredAddrsInfo.ID.Pretty(), err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Discovered and connected successfully to peer " + discoveredAddrsInfo.ID.Pretty()))
}

// handler for the /send/{peerID} endpoint
func (c *P2PClient) sendMessageHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		log.Errorf("Invalid request. Parts: %v", parts)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	peerIDStr := parts[2]
	peerID, err := peer.Decode(peerIDStr)
	if err != nil {
		log.Errorf("Failed to decode peer ID: %s", err)
		http.Error(w, "Invalid peer ID", http.StatusBadRequest)
		return
	}

	// Read the message from the request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	message := string(bodyBytes)

	err = c.sendMessage(peerID, message)
	if err != nil {
		http.Error(w, "Failed to send message", http.StatusInternalServerError)
		return
	}
	log.Infof("Sent message: '%s' to peer %s", message, peerID.String())
	w.Write([]byte("Message sent successfully"))

}

// NewServer creates a new http server with the available handlers
func (c *P2PClient) newServer() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/dhtpeers", c.dhtPeersHandler)
	mux.HandleFunc("/nodepeers", c.nodePeersHandler)
	mux.HandleFunc("/connect/", c.connectHandler)
	mux.HandleFunc("/send/", c.sendMessageHandler)
	mux.HandleFunc("/discover/", c.discoverHandler)
	return mux
}
