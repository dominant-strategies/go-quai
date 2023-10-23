

# 0.1.1 - 2023-10-19

* Added git hook to disable external loggers (logrus, log, etc) before committing
* Added git hook to ensure CHANGELOG update before pushing to remote
* Added libp2p gossipsub protocol
* Added a chat app for testing libp2p gossipsub
* Added a routed host wrapper (`rnode := routedhost.Wrap(node, p2pNode.dht)`) to improve node discoverability
* Added configuration file to load different node settings (`config/config.yaml`)
* Added options to enable new NAT features when instantiating the node:
  * hole punching: `libp2p.EnableHolePunching()`
  * auto relay: `libp2p.EnableAutoRelayWithStaticRelays(staticRelaysAddr)`
  * node relay: `libp2p.EnableRelay()`
* Added mDNS discovery service
* Added a logger option to write logs to a file using `lumberjack`. Logs are written to `nodelogs/` folder
* Added a `node.info` file to store the node's CID and listening addresses
* Updated the `dev-notes.md` file with new instructions
* Changed DHT config to startup in server mode (`kadht.Mode(kadht.ModeServer)`)
* Created new methods to start and stop the node.
* Added new command line flags
  * `-b` to specify a list of bootstrap addresses
  * `-s` to start the node as a server (no DHT bootstrap)
  

# 0.1.0 - 2023-10-10

* Initial release