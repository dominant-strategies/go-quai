package requestManager

import (
	"crypto/rand"
	"errors"
	"sync"

	"github.com/dominant-strategies/go-quai/log"
)

var (
	errRequestNotFound = errors.New("request not found")
)

type RequestManager interface {
	CreateRequest() (id uint32)
	CloseRequest(id uint32)
	GetRequestChan(id uint32) (dataChan chan interface{}, err error)
}

// RequestIDManager is a singleton that manages request IDs
type requestIDManager struct {
	mu             sync.Mutex
	activeRequests map[uint32]chan interface{}
}

// Returns the singleton RequestIDManager
func NewManager() RequestManager {
	return &requestIDManager{
		mu:             sync.Mutex{},
		activeRequests: make(map[uint32]chan interface{}),
	}
}

// Generates a new random uint32 as request ID
func (m *requestIDManager) CreateRequest() uint32 {
	var id uint32
	for {
		b := make([]byte, 4)
		_, err := rand.Read(b)
		if err != nil {
			log.Global.Warnf("failed to generate random request ID: %s . Retrying...", err)
			continue
		}
		id = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		if _, ok := m.activeRequests[id]; !ok{
			break
		}
	}

	m.addRequestID(id, make(chan interface{}))
	return id
}

// Adds a request ID to the active requests map
func (m *requestIDManager) addRequestID(id uint32, dataChan chan interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests[id] = dataChan
}

// Removes a request ID from the active requests map
func (m *requestIDManager) CloseRequest(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dataChan := m.activeRequests[id]
	close(dataChan)
	delete(m.activeRequests, id)
}

// Checks if a request ID exists in the active requests map
func (m *requestIDManager) GetRequestChan(id uint32) (chan interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dataChan, ok := m.activeRequests[id]
	if !ok {
		return nil, errRequestNotFound
	}
	return dataChan, nil
}
