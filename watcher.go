package main

import (
	"context"
	log "github.com/Sirupsen/logrus"
	"github.com/gomodule/redigo/redis"
)

// A Watcher watches Redis for keyspace events.
// Watchers are NOT safe for use from concurrent goroutines.
type Watcher struct {
	PS redis.PubSubConn
}

// Subscribe watches a given key.
func (w *Watcher) Subscribe(db int, key string) {
	w.PS.Subscribe(TopicForKeyspaceEvent(db, key))
}

// Unsubscribe undoes a previous Subscribe().
func (w *Watcher) Unsubscribe(db int, key string) {
	w.PS.Unsubscribe(TopicForKeyspaceEvent(db, key))
}

// Run returns a pipeline of keys that are subscribed, unsubscribed or modified.
func (w *Watcher) Run(ctx context.Context) <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)

		for {
			switch v := w.PS.Receive().(type) {
			case redis.Subscription:
				ch <- KeyFromKeyspaceTopic(v.Channel)
			case redis.Message:
				ch <- KeyFromKeyspaceTopic(v.Channel)
			case error:
				select {
				case <-ctx.Done():
					return
				default:
					log.WithError(v).Error("[Watcher] Receive failed")
				}
			}
		}
	}()

	return ch
}
