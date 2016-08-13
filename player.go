package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"golang.org/x/net/context"
	"gopkg.in/redsync.v1"
	"sync"
	"time"
)

// The Player subsystem watches Redis for tracks to play, and carries out this most important of
// duties. Under no circumstances must the Player communicate directly with a user; that's the
// Responder's job.
type Player struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	// Distributed lock manager using the redsync algorithm.
	redsync *redsync.Redsync

	// Active voice connections keyed by GID.
	vcCancels   map[string]context.CancelFunc
	vcMutex     sync.Mutex
	vcWaitGroup sync.WaitGroup

	// Pub/Sub connection that watches servers' `state` key; use the mutex when issuing commands.
	// (Subscriptions are handled by HandleGuildCreate and HandleGuildDelete.)
	stateWatchPS    redis.PubSubConn
	stateWatchMutex sync.Mutex

	// If we're shutting down, we don't want to go starting new tracks.
	isShuttingDown bool
}

func NewPlayer(s *discordgo.Session, p *redis.Pool) *Player {
	return &Player{
		Session:   s,
		Pool:      p,
		redsync:   redsync.New([]redsync.Pool{p}),
		vcCancels: make(map[string]context.CancelFunc),
	}
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

	// Wait for the context to terminate, then mark the player as shutting down. This will prevent
	// it from accepting any new tracks.
	<-ctx.Done()
	p.isShuttingDown = true

	// Wait for all currently playing tracks to finish playing.
	p.vcWaitGroup.Wait()
}

// HandleGuildCreate handles guild creation events. A batch of these are sent on startup, and the
// list of available servers is to be considered Eventually Consistent(tm).
func (p *Player) HandleGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	p.stateWatchMutex.Lock()
	p.stateWatchPS.Subscribe(TopicForKeyspaceEvent(0, KeyForServerState(g.ID)))
	p.stateWatchMutex.Unlock()

	// Make sure the guild's state is being carried out properly.
	p.Fulfill(g.Guild)
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
			}).Info("Player: ListenForStateKeyChanges: Message received")

			gid := GIDFromKeyspaceEventTopic(v.Channel)
			g, err := p.Session.State.Guild(gid)
			if err != nil {
				log.WithError(err).Error("Player: ListenForStateKeyChanges: Couldn't look up guild")
				break
			}
			go p.Fulfill(g)
		case redis.Subscription:
			log.WithFields(log.Fields{
				"chan":  v.Channel,
				"count": v.Count,
				"kind":  v.Kind,
			}).Info("Player: ListenForStateKeyChanges: Subscription")
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

// Fulfill looks at the recorded state for a guild in Redis, and ensures that it
// corresponds to the actual state.
func (p *Player) Fulfill(g *discordgo.Guild) {
	rconn := p.Pool.Get()
	defer rconn.Close()

	// If there's nothing playing in this guild, just ignore it.
	state, err := redis.String(rconn.Do("GET", KeyForServerState(g.ID)))
	if err != nil && err != redis.ErrNil {
		log.WithError(err).WithField("gid", g.ID).Error("Player: Fulfill: Couldn't get guild state")
		return
	}

	switch state {
	case "", "stopped":
		// If nothing should be playing, shut down any player we hold in this guild.
		p.vcMutex.Lock()
		if cancel := p.vcCancels[g.ID]; cancel != nil {
			cancel()
		}
		p.vcMutex.Unlock()
	case "playing":
		// If there should be something playing, but we're already doing that, do nothing.
		p.vcMutex.Lock()
		if _, ok := p.vcCancels[g.ID]; ok {
			log.WithField("gid", g.ID).Info("Player: Fulfill: Already playing in guild")
			p.vcMutex.Unlock()
			break
		}
		p.vcMutex.Unlock()

		// If we're not playing anything, but should, try to acquire the guild's player lock. This
		// will be retried until it succeeds, under the assumption that any wait is caused by
		// an old player instance that's waiting for a song to finish playing before shutting down.
		mutex := p.redsync.NewMutex(KeyForServerPlayerLock(g.ID), redsync.SetExpiry(15*time.Second), redsync.SetTries(1))
		for {
			if err = mutex.Lock(); err != nil {
				log.WithError(err).WithField("gid", g.ID).Warn("Player: Fulfill: Couldn't aquire lock")
				time.Sleep(2 * time.Second)
				continue
			}
			break
		}
		log.WithField("gid", g.ID).Info("Player: Fulfill: Lock claimed")

		// Spawn a player goroutine to handle the rest.
		ctx, cancel := context.WithCancel(context.Background())
		p.vcMutex.Lock()
		p.vcCancels[g.ID] = cancel
		p.vcMutex.Unlock()
		go p.Play(ctx, g, mutex)
	}
}

func (p *Player) Play(ctx context.Context, g *discordgo.Guild, mutex *redsync.Mutex) {
	extendTicker := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-ctx.Done():
			mutex.Unlock()
			return
		case <-extendTicker.C:
			if !mutex.Extend() {
				log.Error("Player: Play: Failed to extend mutex!")
				return
			}
		}
	}
}
