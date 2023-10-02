# Design goals for the go-quai client
At a high level, the go-quai client must do the following things:
* Join the Quai P2P network
* Download old blocks to sync with peers
* Relay new transactions and blocks to peers
* Accept blocks according to Quai protocol rules
* Build new blocks to participate in consensus
* Database for persistent block storage
* Node API to provide a Quai interface for external tools

## P2P Networking
go-quai makes extensive use of the [libp2p](https://github.com/libp2p/go-libp2p) library to implement a P2P network. The libp2p runtime is the top routine for the go-quai process. All other routines are implemented as subroutines of the libp2p client.

### P2P Design Goals
* bootstrap to user-specifiable boot peers
* kademlia DHT for peer discovery
* direct connection upgrade to peers behind NAT via rendezvous peers
* gossip router for transaction and block data

## Synchronization

## Consensus

## Mining

## Storage

## Node API
