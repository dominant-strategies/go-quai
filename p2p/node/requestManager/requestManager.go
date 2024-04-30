package requestManager

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/log"
)

const (
	C_requestTimeout = 10 * time.Second
)

var (
	errRequestNotFound = errors.New("request not found")
)

type RequestManager interface {
	CreateRequest() (id uint32)
	CloseRequest(id uint32)
	GetRequestChan(id uint32) (dataChan chan interface{}, err error)
}

type requestIDMap struct {
	sync.Map
}

func (m *requestIDMap) store(id uint32, dataChan chan interface{}) {
	m.Map.Store(id, dataChan)
}

func (m *requestIDMap) load(id uint32) (chan interface{}, bool) {
	dataChan, ok := m.Map.Load(id)
	if !ok {
		return nil, false
	}
	return dataChan.(chan interface{}), true
}

func (m *requestIDMap) delete(id uint32) {
	m.Map.Delete(id)
}

// RequestIDManager is a singleton that manages request IDs
type requestIDManager struct {
	mu             sync.Mutex
	activeRequests *requestIDMap
}

// Returns the singleton RequestIDManager
func NewManager() RequestManager {
	return &requestIDManager{
		mu:             sync.Mutex{},
		activeRequests: new(requestIDMap),
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
		if _, ok := m.activeRequests.load(id); !ok {
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
	m.activeRequests.store(id, dataChan)
}

// Removes a request ID from the active requests map
func (m *requestIDManager) CloseRequest(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests.delete(id)
}

// Checks if a request ID exists in the active requests map
func (m *requestIDManager) GetRequestChan(id uint32) (chan interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dataChan, ok := m.activeRequests.load(id)
	if !ok {
		return nil, errRequestNotFound
	}
	return dataChan, nil
}
