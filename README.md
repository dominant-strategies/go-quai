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
go build -o ./build/go-quai main.go
```

### Running a node
To run a node with default options, simply execute:
```shell
./build/go-quai start
```

There are several options and subcommands available when running the go-quai client. See the help menu for a complete list:
```shell
./build/go-quai --help
```

Any options may be provided as command-line arguments or specified in the config file at `config.yaml`.

### Running tests
To run the included unit tests, run the following command:
```
./build/go-quai test
```

## Contributing
We welcome community contributions! If you find a bug, have a feature request, or would like to help out with development, we would love to hear from you; no fix is too small. Please take a look at `[CONTRIBUTING.md](CONTRIBUTING.md)` for guidelines for contributing to the project. 

## License
This software is licensed under the GNU Genreral Public License, Version 3. See [LICENSE](LICENSE) for details.
