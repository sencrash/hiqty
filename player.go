package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"golang.org/x/net/context"
	"sync"
)

// The Player subsystem watches Redis for tracks to play, and carries out this most important of
// duties. Under no circumstances must the Player communicate directly with a user; that's the
// Responder's job.
type Player struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	// Pub/Sub connection that watches servers' `state` key; use the mutex when issuing commands.
	// (Subscriptions are handled by HandleGuildCreate and HandleGuildDelete.)
	stateWatchPS    redis.PubSubConn
	stateWatchMutex sync.Mutex
}

// Run runs the player. It will not return until both the context has finished, and there are no
// more tracks currently playing.
func (p *Player) Run(ctx context.Context) {
	// Make sure keyspace notifications are enabled, then create the state watcher out of that.
	stateWatchRConn := p.Pool.Get()
	_, err := stateWatchRConn.Do("CONFIG", "SET", "notify-keyspace-events", "AKE")
	if err != nil {
		log.WithError(err).Error("Player: Couldn't enable keyspace events; state watching will not work!")
		return
	}
	p.stateWatchPS = redis.PubSubConn{Conn: stateWatchRConn}
	// This hangs for some reason?
	// defer p.stateWatchPS.Close()
	go p.ListenForStateKeyChanges(ctx)

	// Add Discord handlers.
	deregGuildCreate := p.Session.AddHandler(p.HandleGuildCreate)
	defer deregGuildCreate()
	deregGuildDelete := p.Session.AddHandler(p.HandleGuildDelete)
	defer deregGuildDelete()

	// Wait for the context to terminate.
	<-ctx.Done()
}

// HandleGuildCreate handles guild creation events. A batch of these are sent on startup, and the
// list of available servers is to be considered Eventually Consistent(tm).
func (p *Player) HandleGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	p.stateWatchMutex.Lock()
	defer p.stateWatchMutex.Unlock()

	p.stateWatchPS.Subscribe(TopicForKeyspaceEvent(0, KeyForServerState(g.ID)))
}

// HandleGuildDelete handles guild deletion events. This is also sent when the bot is ejected from
// a server for any reason, so here's the place to really purge it.
func (p *Player) HandleGuildDelete(_ *discordgo.Session, g *discordgo.GuildDelete) {
	// Unsubscribe from events for it
	p.stateWatchMutex.Lock()
	p.stateWatchPS.Unsubscribe(TopicForKeyspaceEvent(0, KeyForServerState(g.ID)))
	p.stateWatchMutex.Unlock()

	// Nuke all server-related keys from Redis
	match := KeyForServer(g.ID, "*")
	log.WithField("match", match).Info("Player: GuildDelete: Cleaning redis...")

	cursor := int64(0)
	rconn := p.Pool.Get()
	defer rconn.Close()
	for {
		values, err := redis.Values(rconn.Do("SCAN", cursor, "MATCH", match, "COUNT", 100))
		if err != nil {
			log.WithError(err).Error("Player: GuildDelete: Couldn't scan server-related keys!")
			return
		}

		var keys [][]byte
		if _, err := redis.Scan(values, &cursor, &keys); err != nil {
			log.WithError(err).Error("Player: GuildDelete: Couldn't decode response!")
			return
		}

		if len(keys) > 0 {
			delArgs := make([]interface{}, len(keys))
			for i, key := range keys {
				delArgs[i] = key
			}
			if _, err := redis.Int(rconn.Do("DEL", delArgs...)); err != nil {
				log.WithError(err).Error("Player: GuildDelete: Couldn't delete key batch!")
				return
			}
		}

		if cursor == 0 {
			break
		}
	}
}

// ListenForStateKeyChanges listens for changes to watched 'state' keys.
func (p *Player) ListenForStateKeyChanges(ctx context.Context) {
	for {
		switch v := p.stateWatchPS.Receive().(type) {
		case redis.Message:
			log.WithFields(log.Fields{
				"chan": v.Channel,
				"data": string(v.Data),
			}).Info("Received Message")
		case redis.Subscription:
			log.WithFields(log.Fields{
				"chan":  v.Channel,
				"count": v.Count,
				"kind":  v.Kind,
			}).Info("Subscription")
		case error:
			// This may not be a real error - we may be shutting down
			select {
			case <-ctx.Done():
				return
			default:
				log.WithError(v).Error("Couldn't receive key changes")
			}
		}
	}
}
