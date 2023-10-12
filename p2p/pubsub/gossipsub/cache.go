package gossipsub

import (
	"time"

	lru "github.com/hashicorp/golang-lru"
)

const cacheSize = 1000

// Gossipsub message cache.
// Mostly used for duplicate messages and handling of recently seen messages.
var msgCache *lru.Cache

func init() {
	msgCache, _ = lru.NewWithEvict(cacheSize, onEvicted)
}

func AddMessageToCache(msgID string, msgData interface{}) {
	msgCache.Add(msgID, msgData)
}

func GetMessageFromCache(msgID string) (interface{}, bool) {
	return msgCache.Get(msgID)
}

func onEvicted(key interface{}, value interface{}) {
	// TODO: Handle any logic needed when a message is evicted from cache
}

func CleanupCache(tickDuration time.Duration) {
	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()

	for range ticker.C {
		// TODO: Add cleanup logic
	}
}
