package notifications

import (
	"sync"

	"github.com/redis/go-redis/v9"
)

// Broker fans out bell wake-ups to per-user subscribers (user id is platform UUID or legacy id string).
//
// Subscribers are in-process. With a Redis relay attached (EnableRedis), a
// Publish is announced to every instance and each delivers to its own
// subscribers; without one, Publish reaches only this instance.
type Broker struct {
	mu   sync.Mutex
	subs map[string][]chan struct{}

	relay *redis.Client
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[string][]chan struct{})}
}

func (b *Broker) Subscribe(userID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[userID] = append(b.subs[userID], ch)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[userID]
		for i, c := range list {
			if c == ch {
				b.subs[userID] = append(list[:i], list[i+1:]...)
				if len(b.subs[userID]) == 0 {
					delete(b.subs, userID)
				}
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

// Publish wakes every bell stream for userID. With a relay the wake-up goes
// out through Redis and comes back to this instance via its own subscription,
// so it is not also delivered locally here (that would wake local streams
// twice).
func (b *Broker) Publish(userID string) {
	if b.relay != nil && b.publishRemote(userID) {
		return
	}
	b.deliver(userID)
}

// deliver wakes this instance's subscribers for userID.
func (b *Broker) deliver(userID string) {
	b.mu.Lock()
	subs := append([]chan struct{}(nil), b.subs[userID]...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
