# This is WIP

## available commands

```bash
go-quai start

go-quai help
```
## available flags

For complete options see the 'help' dialogue:

```bash
go-quai --help
  ```

## starting the node

```bash
go run main.go start -l debug -p 4002 -t 8081 -k ../priv.key
```
## http endpoints

### /discover/{peerId}
Discovers and connects to a peer

```bash
❯ curl http://127.0.0.1:8080/discover/12D3KooWAwgaeQwgWSdqj9m76wALTz9rpKDGBP6grxQ7hzA2ZVCk
Discovered and connected successfully to peer 12D3KooWAwgaeQwgWSdqj9m76wALTz9rpKDGBP6grxQ7hzA2ZVCk
```

### /send/{peerId}
Sends a message to a peer

```bash
curl -X POST http://127.0.0.1:8080/send/12D3KooWAwgaeQwgWSdqj9m76wALTz9rpKDGBP6grxQ7hzA2ZVCk -d "Hello from peer #1"
Message sent successfully
```

### /nodepeers
Lists all the peers connected to the node

```bash
curl -X POST http://127.0.0.1:8080/nodepeers
12D3KooWSU15KT6Q3uzsVG2Jixa9tprzGzdtMN7d7zKVBvVstTw3
12D3KooWJn4G4MR8oWy99ss4wZM5BJuhkto1Rt4QD34QopVECZDE
...
12D3KooWAsst1B28BmudgEJy6yNmQqXxrLKdfWrMVAJNnZcrmSCL
```bash