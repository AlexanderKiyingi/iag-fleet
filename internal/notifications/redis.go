package notifications

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cross-instance bell wake-ups.
//
// The producer runs on every API instance and Upsert dedups the notification
// row, so exactly one instance "creates" it and calls Publish. Before the relay
// that woke only the bell streams held by that instance: a user whose SSE/WS
// landed on another replica saw nothing until they reloaded. No error, nothing
// in the logs — the bell is just quiet.
//
// The fix does not need to carry the notification. Every instance reads the
// same database, so the message is only the user id — a nudge, not a payload —
// and each instance re-reads the list for its own streams. Same design as
// contract-management's workspace NudgeBridge.
const wakeChannel = "fleet:notifications:wake"

// EnableRedis routes Publish through Redis so wake-ups reach every instance,
// and starts the subscriber that delivers them to this instance's streams.
// Call once at boot, before the broker is shared. The subscriber returns when
// ctx is done; go-redis reconnects it on its own if Redis drops.
func (b *Broker) EnableRedis(ctx context.Context, client *redis.Client) {
	if b == nil || client == nil {
		return
	}
	b.relay = client
	go b.runSubscriber(ctx)
}

// publishRemote announces a wake-up for userID. Reports false when Redis
// refused it, in which case the caller delivers locally — at worst this
// behaves exactly as it did before the relay existed.
func (b *Broker) publishRemote(userID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.relay.Publish(ctx, wakeChannel, userID).Err(); err != nil {
		slog.Warn("bell wake publish failed; waking local streams only", "err", err)
		return false
	}
	return true
}

func (b *Broker) runSubscriber(ctx context.Context) {
	pubsub := b.relay.Subscribe(ctx, wakeChannel)
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.Payload != "" {
				b.deliver(msg.Payload)
			}
		}
	}
}
