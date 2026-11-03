package notifications

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Two brokers sharing one Redis stand in for two replicas: a wake-up published
// on one must reach a bell stream held by the other, and the publisher's own
// streams must be woken exactly once (via the subscription, not also locally).
//
// Needs a throwaway Redis: REDIS_TEST_URL=redis://127.0.0.1:56379/0
func TestBrokerRedisRelayCrossInstance(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("REDIS_TEST_URL not set")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	newClient := func() *redis.Client {
		c := redis.NewClient(opt)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	probe := newClient()
	if err := probe.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis: %v", err)
	}

	a, b := NewBroker(), NewBroker()
	a.EnableRedis(ctx, newClient())
	b.EnableRedis(ctx, newClient())
	waitForSubscribers(t, probe, 2)

	onA, stopA := a.Subscribe("user-1")
	defer stopA()
	onB, stopB := b.Subscribe("user-1")
	defer stopB()
	other, stopOther := b.Subscribe("user-2")
	defer stopOther()

	// Counting goroutines rather than reading the buffered channel directly:
	// the channel holds one wake-up, so a second delivery would be coalesced
	// and a double-wake would go unseen.
	countA, countB, countOther := count(onA), count(onB), count(other)

	a.Publish("user-1")
	time.Sleep(300 * time.Millisecond)

	if n := countA(); n != 1 {
		t.Fatalf("publishing instance woke its own stream %d times, want 1", n)
	}
	if n := countB(); n != 1 {
		t.Fatalf("other instance woke %d times, want 1", n)
	}
	if n := countOther(); n != 0 {
		t.Fatalf("unrelated user woke %d times, want 0", n)
	}
}

// Without Redis the broker keeps its original single-instance behaviour.
func TestBrokerLocalOnly(t *testing.T) {
	b := NewBroker()
	ch, stop := b.Subscribe("u")
	defer stop()
	b.Publish("u")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("local wake-up not delivered")
	}
}

// count drains ch in the background and returns a reader for how many
// wake-ups arrived so far.
func count(ch <-chan struct{}) func() int {
	n := make(chan int, 1)
	n <- 0
	go func() {
		for range ch {
			v := <-n
			n <- v + 1
		}
	}()
	return func() int {
		v := <-n
		n <- v
		return v
	}
}

func waitForSubscribers(t *testing.T, c *redis.Client, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.PubSubNumSub(context.Background(), wakeChannel).Result()
		if err == nil && res[wakeChannel] >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("redis subscribers on %s never reached %d", wakeChannel, want)
}

// A Redis that is down must not lose the wake-up for this instance's streams.
func TestBrokerRedisDownFallsBackToLocal(t *testing.T) {
	b := NewBroker()
	b.relay = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = b.relay.Close() })
	ch, stop := b.Subscribe("u")
	defer stop()
	b.Publish("u")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("local wake-up not delivered when Redis is down")
	}
}
