package rpc

import (
	"encoding/json"
	"log"
	"net"
	"strconv"
	"sync"
	"bufio"
	"time"
	"io"
)

type MinerSession struct {
	proto 		string
	ip   		string
	port 		string
	conn 		*net.TCPConn
	enc			*json.Encoder

	// Stratum
	sync.Mutex
	latestId	uint64
	timeout		time.Duration
}

const (
	MAX_REQ_SIZE = 1024
)

func NewMinerConn(endpoint string) *MinerSession {
	addr, err := net.ResolveTCPAddr("tcp", endpoint)
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

	ip, port, _ := net.SplitHostPort(conn.RemoteAddr().String())

	return &MinerSession{proto: "tcp", ip: ip, port: port, conn: conn}
}

func (miner *MinerSession) ListenTCP() {
	var accept = make(chan int, 1)
	n := 0

	accept <- n
	go func(ms *MinerSession) {
		err := ms.handleTCPClient(ms)
		if err != nil {
			ms.conn.Close()
		}
		<-accept
	}(miner)

}

func (miner *MinerSession) handleTCPClient(ms *MinerSession) error {
	ms.enc = json.NewEncoder(ms.conn)
	connbuff := bufio.NewReaderSize(ms.conn, MAX_REQ_SIZE)
	// s.setDeadline(cs.conn)
	for {
		data, isPrefix, err := connbuff.ReadLine()
		if isPrefix {
			log.Printf("Socket flood detected from %s", miner.ip)
			// mienr.policy.BanClient(cs.ip)
			return err
		} else if err == io.EOF {
			log.Printf("Client %s disconnected", miner.ip)
			// s.removeSession(miner)
			break
		} else if err != nil {
			log.Printf("Error reading from socket: %v", err)
			return err
		}

		if len(data) > 1 {
			var req StratumReq
			err = json.Unmarshal(data, &req)
			if err != nil {
				// s.policy.ApplyMalformedPolicy(cs.ip)
				log.Printf("Malformed stratum request from %s: %v", ms.ip, err)
				return err
			}
			// s.setDeadline(cs.conn)
			err = ms.handleTCPMessage(s, &req)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (ms *MinerSession) sendTCPResult(result json.RawMessage) error {

	ms.Lock()
	defer ms.Unlock()

	// ms.latestId += 1

	message, err := json.Marshal(jsonrpcMessage{ID: json.RawMessage(strconv.FormatUint(ms.latestId, 10)), Version: "2.0", Error: nil, Result: result})
	if err != nil {
		return err
	}

	ms.conn.Write(message)
	return nil
}

func (ms *MinerSession) sendTCPRequest(msg jsonrpcMessage) error {

	ms.Lock()
	defer ms.Unlock()

	ms.latestId += 1
	message, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	ms.conn.Write(message)
	return nil
}

func (ms *MinerSession) handleTCPMessage(req *StratumReq) error {
	// Handle RPC methods
	// switch req.Message.Method {
	// case "quai_getPendingHeader":
	// 	// reply, errReply := s.handleGetWorkRPC(cs)
	// 	reply, err := api.
	// 	if errReply != nil {
	// 		return cs.sendTCPError(req.Id, errReply)
	// 	}
	// 	return cs.sendTCPResult(req.Id, &reply)
	// }
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
