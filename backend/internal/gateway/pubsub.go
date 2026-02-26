package gateway

import (
	"context"
	"log"
)

// PubSubRouter manages Redis PubSub subscriptions and routes messages
// to the broadcaster for fan-out to WebSocket clients.
type PubSubRouter struct {
	hub *Hub
}

// NewPubSubRouter creates a PubSubRouter backed by the given Hub.
func NewPubSubRouter(hub *Hub) *PubSubRouter {
	return &PubSubRouter{hub: hub}
}

// RunExplicit subscribes to explicitly listed channels and routes messages.
// Blocks until ctx is cancelled.
func (r *PubSubRouter) RunExplicit(ctx context.Context) {
	channels := r.hub.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no explicit channels to subscribe to")
		return
	}

	pubsub := r.hub.Rdb.Subscribe(ctx, channels...)
	defer pubsub.Close()

	log.Printf("[api_gateway] subscribed to %d PubSub channels", len(channels))

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			r.hub.broadcast(msg.Channel, []byte(msg.Payload))
		}
	}
}

// RunPattern subscribes to wildcard patterns for dynamic indicator channels.
// Blocks until ctx is cancelled.
func (r *PubSubRouter) RunPattern(ctx context.Context) {
	pubsub := r.hub.Rdb.PSubscribe(ctx, "pub:ind:*", "pub:tick:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			r.hub.broadcast(msg.Channel, []byte(msg.Payload))
		}
	}
}
