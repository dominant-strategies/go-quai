## Go Quai

Official Golang implementation of the Quai protocol.

[![API Reference](
https://camo.githubusercontent.com/915b7be44ada53c290eb157634330494ebe3e30a/68747470733a2f2f676f646f632e6f72672f6769746875622e636f6d2f676f6c616e672f6764646f3f7374617475732e737667
)](https://pkg.go.dev/github.com/dominant-strategies/go-quai/common)
[![Go Report Card](https://goreportcard.com/badge/github.com/dominant-strategies/go-quai)](https://goreportcard.com/report/github.com/dominant-strategies/go-quai)
[![Discord](https://img.shields.io/badge/discord-join%20chat-blue.svg)](https://discord.gg/ngw88VXXnV)

## Building the source

For prerequisites and detailed build instructions please read the [Installation Instructions](https://docs.quai.network/develop/installation).

First, clone the repository and navigate to it using
```shell
$ git clone https://github.com/dominant-strategies/go-quai.git
$ cd go-quai
```

Next, you will need to copy some default environment variables to your machine. You can do this by running 

```shell
$ cp network.env.dist network.env
```

Building `go-quai` requires both a Go (version 1.19 or later) and a C compiler. You can install
them using your favorite package manager. Once these dependencies are installed, run

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

|    Command    | Description |
| :-----------: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
|  **`go-quai`**   | Our main Quai CLI client. It is the entry point into the Quai network (main-, test- or private net), capable of running as a full node (default), archive node (retaining all historical state) or a light node (retrieving data live). It can be used by other processes as a gateway into the Quai network via JSON RPC endpoints exposed on top of HTTP, WebSocket and/or IPC transports. `go-quai --help` for command line options.|
|  **`test`** | Runs a battery of tests on the repository to ensure it builds and functions correctly.|

## Running `go-quai`

### Full node on the main Quai network

Using the makefile will preload configuration values from the `network.env` file.
```shell
$ make run-all
```

### Full node on the Garden test network
Garden test network is based on the Blake3 proof-of-work consensus algorithm. As such,
it has certain extra overhead and is more susceptible to reorganization attacks due to the
network's low difficulty/security.

### Viewing logs
Logs are stored in the `go-quai/nodelogs` directory by default. You can view them by using tail or another utility, like so:
```shell
$ tail -f nodelogs/zone-0-0.log
```

Modify the `network.env` configuration file to reflect:
`NETWORK=garden`. You should also set `ENABLE_ARCHIVE=true` to make sure to save the trie-nodes after you stop your node. Then build and run with the same commands as mainnet.

### Configuration

Configuration is handled in `network.env.dist` file. You will need to copy or rename the file to `network.env`. The make commands will automatically pull from this file for configuration changes.

## Contribution

Thank you for considering to help out with the source code! We welcome contributions
from anyone on the internet, and are grateful for even the smallest of fixes!

If you'd like to contribute to go-quai, please fork, fix, commit and send a pull request
for the maintainers to review and merge into the main code base. If you wish to submit
more complex changes though, please check up with the core devs first on [our Discord Server](https://discord.gg/Nd8JhaENvU)
to ensure those changes are in line with the general philosophy of the project and/or get
some early feedback which can make both your efforts much lighter as well as our review
and merge procedures quick and simple.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting)
   guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary)
   guidelines.
 * Pull requests need to be based on and opened against the `main` branch.
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "rpc: make trace configs optional"

Please see the [Developers' Guide](https://docs.quai.network/contributors/contribute)
for more details on configuring your environment, managing project dependencies, and
testing procedures.

## License

The go-quai library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html),
also included in our repository in the `COPYING.LESSER` file.

The go-quai binaries (i.e. all code inside of the `cmd` directory) is licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also
included in our repository in the `COPYING` file.
