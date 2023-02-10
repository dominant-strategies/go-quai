package rpc

import (
	"context"
	"io"
	"log"
	"net"
	base_rpc "net/rpc"
)

type TCPClient struct {
	endpoint	string
}

func (miner *TCPClient) ListenTCP() {
	addr, err := net.ResolveTCPAddr("tcp", miner.endpoint)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	server, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer server.Close()

	log.Printf("New TCP client from: %v", addr)

	conn, err := server.AcceptTCP()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	conn.SetKeepAlive(true)

	


}

// type tcpConn struct {
// 	in	io.Reader
// 	out	io.Writer
// }

// func DialTCP(ctx context.Context, endpoint string) (*Client, error) {
// 	return DialTCPIO(ctx, endpoint, tcpConn)
// 	// base_rpc.Dial("tcp", endpoint)

// 	// new_client, err :=
// 	// new_client, err := newClient(ctx, func(_ context.Context) (ServerCodec, error) {
// 	// 	return NewCodec(stdioConn{
// 	// 		in: in,
// 	// 		out: out,
// 	// 	}), nil
// 	// })

// 	// return new_client, err
// }

// func DialTCPIO(ctx context.Context, in io.Reader, out io.Writer) (*Client, error) {
// 	return nil, nil
// }