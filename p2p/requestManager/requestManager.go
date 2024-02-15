package requestManager

import (
	"crypto/rand"
	"sync"

	"github.com/dominant-strategies/go-quai/log"
)

var (
	manager *requestIDManager
	once sync.Once
)

type RequestManager interface {
	GenerateRequestID() uint32
	AddRequestID(uint32)
	RemoveRequestID(uint32)
	CheckRequestIDExists(uint32) bool
}

// RequestIDManager is a singleton that manages request IDs
type requestIDManager struct {
	mu             sync.Mutex
	activeRequests map[uint32]struct{}
}

// Returns the singleton RequestIDManager
func GetRequestIDManager() RequestManager {
	once.Do(func() {
		manager = &requestIDManager{
			activeRequests: make(map[uint32]struct{}),
		}
	})
	return &requestIDManager{
		activeRequests: make(map[uint32]struct{}),
	}
}

// Generates a new random uint32 as request ID
func (m *requestIDManager) GenerateRequestID() uint32 {
	var id uint32
	for {
		b := make([]byte, 4)
		_, err := rand.Read(b)
		if err != nil {
			log.Global.Warnf("failed to generate random request ID: %s . Retrying...", err)
			continue
		}
		id = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		if !m.CheckRequestIDExists(id) {
			break
		}
	}
	return id
}

// Adds a request ID to the active requests map
func (m *requestIDManager) AddRequestID(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests[id] = struct{}{}
}

// Removes a request ID from the active requests map
func (m *requestIDManager) RemoveRequestID(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeRequests, id)
}

// Checks if a request ID exists in the active requests map
func (m *requestIDManager) CheckRequestIDExists(id uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.activeRequests[id]
	return exists
}
