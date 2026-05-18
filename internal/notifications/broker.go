package notifications

import "sync"

// Broker fans out bell wake-ups to per-user subscribers (user id is platform UUID or legacy id string).
type Broker struct {
	mu   sync.Mutex
	subs map[string][]chan struct{}
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

func (b *Broker) Publish(userID string) {
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
