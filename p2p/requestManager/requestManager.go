package requestManager

import (
	"crypto/rand"
	"sync"

	"github.com/dominant-strategies/go-quai/log"
)

// RequestIDManager is a singleton that manages request IDs
type RequestIDManager struct {
	mu             sync.Mutex
	activeRequests map[uint32]struct{}
}

var (
	manager *RequestIDManager
	once    sync.Once
)

// Returns the singleton RequestIDManager
func GetRequestIDManager() *RequestIDManager {
	once.Do(func() {
		manager = &RequestIDManager{
			activeRequests: make(map[uint32]struct{}),
		}
	})
	return manager
}

// Generates a new random uint32 as request ID
func (m *RequestIDManager) GenerateRequestID() uint32 {
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
func (m *RequestIDManager) AddRequestID(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests[id] = struct{}{}
}

// Removes a request ID from the active requests map
func (m *RequestIDManager) RemoveRequestID(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeRequests, id)
}

// Checks if a request ID exists in the active requests map
func (m *RequestIDManager) CheckRequestIDExists(id uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.activeRequests[id]
	return exists
}
