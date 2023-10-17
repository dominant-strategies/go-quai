# Go Quai

The official Golang implementation of the Quai protocol.

[![API Reference](
https://camo.githubusercontent.com/915b7be44ada53c290eb157634330494ebe3e30a/68747470733a2f2f676f646f632e6f72672f6769746875622e636f6d2f676f6c616e672f6764646f3f7374617475732e737667
)](https://pkg.go.dev/github.com/dominant-strategies/go-quai/common)
[![Go Report Card](https://goreportcard.com/badge/github.com/dominant-strategies/go-quai)](https://goreportcard.com/report/github.com/dominant-strategies/go-quai)
[![Discord](https://img.shields.io/badge/discord-join%20chat-blue.svg)](https://discord.gg/s8y8asPwNC)

- [Go Quai](#go-quai)
  - [Prerequisites](#prerequisites)
  - [Building the source](#building-the-source)
  - [Executables](#executables)
    - [go-quai](#go-quai-1)
    - [test](#test)
  - [Running `go-quai`](#running-go-quai)
    - [Configuration](#configuration)
    - [Starting and stopping a node](#starting-and-stopping-a-node)
    - [Viewing logs](#viewing-logs)
    - [Garden test network](#garden-test-network)
  - [Developer notes](#developer-notes)
  - [Contribution](#contribution)
  - [License](#license)

## Prerequisites

* Go v1.21.0+
* Git
* Make

For details on installing prerequisites, visit the [node installation instructions in the Quai Documentation](https://docs.quai.network/node/node-overview/run-a-node).

## Building the source
Once you have the necessary [prerequisites](#prerequisites), clone the `go-quai` repository and navigate to it using:

```shell
$ git clone https://github.com/dominant-strategies/go-quai.git
$ cd go-quai
```

Next, you will need to copy the boilerplate node configuration file (`network.env.dist`) into a configuration file called `network.env`. You can do this by running: 

```shell
$ cp network.env.dist network.env
```

Building `go-quai` requires both a Go (version 1.21.0+ or later) and a C compiler. You can install them using your favorite package manager. Once these dependencies are installed, run:

```shell
$ make go-quai
```

or, to build the full suite of utilities:

```shell
$ make all
```

## Executables

The go-quai project comes with several wrappers/executables found in the `cmd`
directory.

### go-quai

Our main Quai CLI client. go-quai is the entry point into the Quai network (main-, test-, or private net), capable of running as a slice node (single slice), multi-slice node (subset of slices), and a global node (all slices). Each of these types of nodes can be run as full node (default), a light node (retrieving data live), or an archive node (retaining all historical state). 

go-quai can be used by other processes as a gateway into the Quai network via JSON RPC endpoints exposed on top of HTTP, WebSocket and/or IPC transports. 

`go-quai --help` for command line options.

| go-quai Nodes     | Single Slice              | Multiple Slices          | All Slices          |
| :---------------: | :-----------------------: | :----------------------: | :-----------------: |
| **Full Node**     | Slice Full Node (Default) | Multi-Slice Full Node    | Global Full Node    |
| **Light Nodes**   | Slice Light Node          | Multi-Slice Light Node   | Global Light Node   | 
| **Archive Node**  | Slice Archive Node        | Multi-Slice Archive Node | Global Archive Node |

### test

Runs a battery of tests on the repository to ensure it builds and functions correctly.

## Running `go-quai`

### Configuration

Configuration is handled in the `network.env` file. You will need to copy or rename the file to `network.env`. The make commands will automatically pull from this file for configuration changes.

The default configuration of the `network.env` file is for a **global full node** on the `main` (`colosseum`) network.

* The `SLICES` parameter determines which slices of the network the node will run (i.e. determines whether the node will be a slice node, a multi-slice node, or a global node).

* The `COINBASE` paratmeter contains the addresses that mining rewards will be paid to. There is one `COINBASE` address for each slice. 

* The `NETWORK` parameter determines the network that the node will run on. Networks include the mainnet (`colosseum`) and [garden](#garden-test-network) networks.

### Starting and stopping a node

The `go-quai` client can be started by using:

```shell
$ make run
```

Using the makefile will preload configuration values from the `network.env` file.

The `go-quai` client can be stopped by using:

```shell
$ make stop
```

### Viewing logs

Logs are stored in the `go-quai/nodelogs` directory by default. You can view them by using tail or another utility, like so:

```shell
$ tail -f nodelogs/zone-X-Y.log
```

X and Y should be replaced with values between 0-2 to define which slice's logs to display.

### Garden test network

The Garden test network is based on the Blake3 proof-of-work consensus algorithm. As such, it has certain extra overhead and is more susceptible to reorganization attacks due to the network's low difficulty/security.

To run on the Garden test network, modify the `network.env` configuration file to reflect `NETWORK=garden`. You should also set `ENABLE_ARCHIVE=true` to make sure to save the trie-nodes after you stop your node. Then [build](#building-the-source) and [run](#starting-and-stopping-a-node) with the same commands as mainnet.

## Developer notes

See [dev-notes.md](dev-notes.md) for more information on the development of go-quai.

## Contribution

Thank you for considering to help out with the source code! We welcome contributions from anyone on the internet, and are grateful for even the smallest of fixes.

If you'd like to contribute to `go-quai`, please fork, fix, commit and send a pull request for the maintainers to review and merge into the main code base.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting)
   guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary)
   guidelines.
 * Pull requests need to be based on and opened against the `main` branch.
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "rpc: make trace configs optional"

If you wish to submit more complex changes, please check up with the core devs first in the [Quai development Discord Server](https://discord.gg/s8y8asPwNC) to ensure your changes are in line with the general philosophy of the project and/or get some early feedback which can make both your efforts much lighter as well as our review and merge procedures quick and simple.

## License

The go-quai library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html),
also included in our repository in the `COPYING.LESSER` file.

The go-quai binaries (i.e. all code inside of the `cmd` directory) is licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also
included in our repository in the `COPYING` file.
