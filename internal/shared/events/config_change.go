package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ChannelConfigChanged is the Redis Pub/Sub invalidation hint channel.
// Messages are untrusted invalidation hints only; gateways always pull and checksum-verify from control plane.
const ChannelConfigChanged = "gatewayforge:config:changed"

// ConfigChangeHint is a minimal invalidation notification emitted strictly after DB commit.
type ConfigChangeHint struct {
	OrgID       string    `json:"org_id"`
	Version     int       `json:"version"`
	Checksum    string    `json:"checksum"`
	PublishedAt time.Time `json:"published_at"`
}

// Publisher emits config-change invalidation hints to Redis Pub/Sub.
type Publisher struct {
	client  *redis.Client
	channel string
}

// NewPublisher constructs a Redis invalidation hint Publisher.
func NewPublisher(client *redis.Client, channel string) *Publisher {
	if channel == "" {
		channel = ChannelConfigChanged
	}
	return &Publisher{client: client, channel: channel}
}

// PublishHint emits a minimal hint payload to Redis. Failures are returned to caller for logging
// without failing the already-committed PostgreSQL publication.
func (p *Publisher) PublishHint(ctx context.Context, hint ConfigChangeHint) error {
	if p == nil || p.client == nil {
		return nil
	}
	raw, err := json.Marshal(hint)
	if err != nil {
		return fmt.Errorf("marshal config change hint: %w", err)
	}
	return p.client.Publish(ctx, p.channel, raw).Err()
}

// HintCoalescer tracks recently observed hint checksums/versions to coalesce duplicate or reordered
// notifications and emit at most one pending refresh signal into Triggers().
type HintCoalescer struct {
	triggers     chan ConfigChangeHint
	lastChecksum string
}

// NewHintCoalescer constructs a HintCoalescer with a 1-slot coalescing trigger channel.
func NewHintCoalescer() *HintCoalescer {
	return &HintCoalescer{
		triggers: make(chan ConfigChangeHint, 1),
	}
}

// Triggers returns the channel signaling that a snapshot pull should run immediately.
func (c *HintCoalescer) Triggers() <-chan ConfigChangeHint {
	return c.triggers
}

// Accept parses raw pub/sub payload, ignores malformed or exact-duplicate hints, and signals Triggers non-blockingly.
func (c *HintCoalescer) Accept(raw string) bool {
	var hint ConfigChangeHint
	if err := json.Unmarshal([]byte(raw), &hint); err != nil {
		return false
	}
	if hint.Checksum == "" && hint.Version <= 0 {
		return false
	}
	if hint.Checksum != "" && hint.Checksum == c.lastChecksum {
		return false
	}
	c.lastChecksum = hint.Checksum
	select {
	case c.triggers <- hint:
	default:
		// A refresh trigger is already queued; drain and replace with latest hint.
		select {
		case <-c.triggers:
		default:
		}
		select {
		case c.triggers <- hint:
		default:
		}
	}
	return true
}

// Subscribe listens to Redis Pub/Sub invalidation hints until ctx is canceled, reconnecting with bounded backoff.
func Subscribe(ctx context.Context, client *redis.Client, channel string, coalescer *HintCoalescer, logger *slog.Logger) {
	if client == nil || coalescer == nil {
		return
	}
	if channel == "" {
		channel = ChannelConfigChanged
	}
	if logger == nil {
		logger = slog.Default()
	}

	backoff := 200 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		pubsub := client.Subscribe(ctx, channel)
		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			logger.Warn("redis config-change subscribe failed; relying on periodic reconciliation", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < 5*time.Second {
					backoff *= 2
				}
				continue
			}
		}
		backoff = 200 * time.Millisecond

		ch := pubsub.Channel()
		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case msg, ok := <-ch:
				if !ok {
					closed = true
					break
				}
				coalescer.Accept(msg.Payload)
			}
		}
		_ = pubsub.Close()
	}
}
