package stratum

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
)

const (
	testInternalAddress = "0x0012345678901234567890123456789012345678"
	testExternalAddress = "0x2012345678901234567890123456789012345678"
)

// stratumTestBackend serves a zone [0,0] node with no pending header.
type stratumTestBackend struct {
	quaiapi.Backend
}

func (stratumTestBackend) NodeLocation() common.Location { return common.Location{0, 0} }

func (stratumTestBackend) GetPendingHeader(types.PowID, common.Address, []byte, uint8) (*types.WorkObject, error) {
	return nil, errors.New("no pending header in test")
}

func newStratumTestServer() *Server {
	return &Server{
		backend:        stratumTestBackend{},
		logger:         log.Global,
		stats:          NewPoolStats(log.Global),
		sessionsSHA:    make(map[*session]struct{}),
		sessionsScrypt: make(map[*session]struct{}),
		sessionsKawpow: make(map[*session]struct{}),
		conns:          make(map[net.Conn]struct{}),
	}
}

// stratumTestClient is the miner side of a net.Pipe connection to handleConn.
type stratumTestClient struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
	done   chan struct{}
}

func newStratumTestClient(t *testing.T, s *Server) *stratumTestClient {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleConn(server, "sha256")
	}()
	c := &stratumTestClient{t: t, conn: client, reader: bufio.NewReader(client), done: done}
	t.Cleanup(func() {
		client.Close()
		<-done
	})
	return c
}

func (c *stratumTestClient) send(id int, method string, params ...interface{}) {
	c.t.Helper()
	msg, err := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(append(msg, '\n')); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}
}

// response reads messages until it finds the response with the given id.
// Notifications from the server are skipped.
func (c *stratumTestClient) response(id int) stratumResp {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			c.t.Fatalf("read response %d: %v", id, err)
		}
		var resp stratumResp
		if err := json.Unmarshal(line, &resp); err != nil {
			c.t.Fatalf("decode response %q: %v", line, err)
		}
		if gotID, ok := resp.ID.(float64); ok && int(gotID) == id {
			return resp
		}
	}
}

// waitClosed waits until the server closes the connection.
func (c *stratumTestClient) waitClosed() {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, c.reader); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		c.t.Fatalf("connection was not closed: %v", err)
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		c.t.Fatal("handleConn did not return")
	}
}

func registeredUsers(s *Server) []string {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	users := make([]string, 0, len(s.sessionsSHA))
	for sess := range s.sessionsSHA {
		users = append(users, sess.user)
	}
	return users
}

func errorText(resp stratumResp) string {
	if resp.Error == nil {
		return ""
	}
	if text, ok := resp.Error.(string); ok {
		return text
	}
	return ""
}

// H10: a share must not be processed before the session is authorized.
func TestStratumSubmitRequiresAuthorization(t *testing.T) {
	c := newStratumTestClient(t, newStratumTestServer())
	c.send(1, "mining.submit", "worker", "00000001", "00000000", "00000000", "00000000")
	resp := c.response(1)
	if resp.Result != false || !strings.Contains(errorText(resp), "unauthorized") {
		t.Fatalf("got %+v, want an unauthorized rejection", resp)
	}
}

// H10: a rejected re-authorize must keep the previous payout address.
func TestStratumRejectedReauthorizeKeepsPayoutAddress(t *testing.T) {
	s := newStratumTestServer()
	c := newStratumTestClient(t, s)

	c.send(1, "mining.authorize", testInternalAddress+".rig1")
	if resp := c.response(1); resp.Result != true {
		t.Fatalf("valid authorize rejected: %+v", resp)
	}
	c.send(2, "mining.authorize", testExternalAddress+".rig1")
	if resp := c.response(2); resp.Result != false {
		t.Fatalf("external address accepted: %+v", resp)
	}
	users := registeredUsers(s)
	if len(users) != 1 || users[0] != testInternalAddress {
		t.Fatalf("registered payout addresses %v, want [%s]", users, testInternalAddress)
	}
}

// H8: each authorize builds a job from the pending header, so the number of
// authorize requests per session is limited.
func TestStratumLimitsAuthorizeRequests(t *testing.T) {
	c := newStratumTestClient(t, newStratumTestServer())
	for i := 1; i <= maxAuthorizationsPerSession; i++ {
		c.send(i, "mining.authorize", testInternalAddress)
		if resp := c.response(i); resp.Result != true {
			t.Fatalf("authorize %d rejected: %+v", i, resp)
		}
	}
	id := maxAuthorizationsPerSession + 1
	c.send(id, "mining.authorize", testInternalAddress)
	if resp := c.response(id); resp.Result != false || !strings.Contains(errorText(resp), "too many") {
		t.Fatalf("got %+v, want a too many authorize rejection", resp)
	}
	c.waitClosed()
}

// H9: a message that never ends must close the connection instead of being
// buffered without limit.
func TestStratumClosesOversizedMessage(t *testing.T) {
	c := newStratumTestClient(t, newStratumTestServer())
	payload := []byte(`{"id":1,"method":"` + strings.Repeat("a", 4*maxStratumMessageSize))
	writeErr := make(chan error, 1)
	go func() {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := c.conn.Write(payload)
		writeErr <- err
	}()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConn did not close the oversized message")
	}
	if err := <-writeErr; err == nil {
		t.Fatal("server read the whole oversized message")
	}
}

// H9: the limit applies to each message, not to the connection.
func TestMessageLimitReaderResetsPerMessage(t *testing.T) {
	msg := `{"id":1,"method":"mining.extranonce.subscribe","params":[]}` + "\n"
	stream := strings.NewReader(strings.Repeat(msg, 1000)) // many times the limit in total
	reader := &messageLimitReader{r: stream, limit: 4 * len(msg)}
	dec := json.NewDecoder(reader)
	for i := 0; i < 1000; i++ {
		var req stratumReq
		if err := dec.Decode(&req); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		reader.reset()
	}

	reader = &messageLimitReader{r: strings.NewReader(`{"id":1,"method":"` + strings.Repeat("a", 1000)), limit: 100}
	var req stratumReq
	if err := json.NewDecoder(reader).Decode(&req); !errors.Is(err, errStratumMessageTooLarge) {
		t.Fatalf("got %v, want %v", err, errStratumMessageTooLarge)
	}
}
