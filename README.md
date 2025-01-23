# Go Quai
The reference implementation of the Quai protocol, written in Go.

[![API Reference](
https://camo.githubusercontent.com/915b7be44ada53c290eb157634330494ebe3e30a/68747470733a2f2f676f646f632e6f72672f6769746875622e636f6d2f676f6c616e672f6764646f3f7374617475732e737667
)](https://pkg.go.dev/github.com/dominant-strategies/go-quai/common)
[![Go Report Card](https://goreportcard.com/badge/github.com/dominant-strategies/go-quai)](https://goreportcard.com/report/github.com/dominant-strategies/go-quai)
[![Discord](https://img.shields.io/badge/discord-join%20chat-blue.svg)](https://discord.gg/s8y8asPwNC)

## Usage
### Building from source
Once you have the necessary [prerequisites](#prerequisites), clone the `go-quai` repository and navigate to it using:

```shell
git clone https://github.com/dominant-strategies/go-quai.git
cd go-quai
make go-quai
```

After a successful build, the binary will be located at `build/bin/go-quai`.

### Running a node
To run a go-quai node, simply execute the `go-quai start` command. Be sure to specify the parameters you wish to use, such as your coinbase address (if you plan on mining), and which slices you wish to participate in.

For example, here is the run command for miner (0x00a3e45aa16163F2663015b6695894D918866d19) in cyprus-1 (zone-0-0) on the "garden" test network:
```shell
./build/bin/go-quai start --node.slices "[0 0]" --node.coinbases "0x00a3e45aa16163F2663015b6695894D918866d19" --node.environment "garden"
```

For the full list of available options and their default values, consult the help menu:
```shell
./build/go-quai --help
```

All configuration options may be supplied in a config file too, located in the directory specified by `--global.config-dir`. Note specified on the command-line will override options specified in the config file.

### Running tests
To run the included unit tests, run the following command:
```
./build/go-quai test
```

## Contributing
We welcome community contributions! If you find a bug, have a feature request, or would like to help out with development, we would love to hear from you; no fix is too small. Please take a look at `[CONTRIBUTING.md](CONTRIBUTING.md)` for guidelines for contributing to the project. 

## License
This software is licensed under the GNU General Public License, Version 3. See [LICENSE](LICENSE) for details.
