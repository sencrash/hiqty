package main

import (
	"context"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"gopkg.in/redsync.v1"
	"sync"
)

// The PlayerController subsystem watches Redis for key changes, and manages Player instances based
// on these. Uses a distributed lock to ensure that no more than one player exists for a server at
// any given time, while crashed instances smoothly fall over on a new one.
type PlayerController struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	redsync *redsync.Redsync
	wg      sync.WaitGroup

	stateWatchPS    redis.PubSubConn
	stateWatchMutex sync.Mutex
}

// Run runs the player controller. When the request ends, no more players will spawn, and existing
// players will finish playing their current tracks before terminating. Use Wait to wait for this.
func (c *PlayerController) Run(ctx context.Context) {
	c.redsync = redsync.New([]redsync.Pool{c.Pool})

	// Add event handlers.
	defer c.Session.AddHandler(c.HandleGuildCreate)()

	// Watch for keyspace notifications.
	keyWatchConn := c.Pool.Get()
	_, err := keyWatchConn.Do("CONFIG", "SET", "notify-keyspace-events", "AKE")
	if err != nil {
		log.WithError(err).Error("Player: Couldn't enable keyspace events; state watching will not work!")
		return
	}
	c.stateWatchPS = redis.PubSubConn{Conn: keyWatchConn}

	gids := c.readGIDsForStateEvents(ctx)
loop:
	for {
		select {
		case gid := <-gids:
			log.WithField("gid", gid).Info("State event")
			c.Fulfill(gid)
		case <-ctx.Done():
			break loop
		}
	}
}

// Wait waits for all running players to finish before returning.
func (c *PlayerController) Wait() {
	c.wg.Wait()
}

// HandleGuildCreate subscribes to state changes when the bot joins a guild.
func (c *PlayerController) HandleGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	c.stateWatchMutex.Lock()
	c.stateWatchPS.Subscribe(TopicForKeyspaceEvent(0, KeyForServerState(g.ID)))
	c.stateWatchMutex.Unlock()
}

// HandleGuildDelete unsubscribes from state changes when the bot is kicked from a guild.
func (c *PlayerController) HandleGuildDelete(_ *discordgo.Session, g *discordgo.GuildDelete) {
	c.stateWatchMutex.Lock()
	c.stateWatchPS.Unsubscribe(TopicForKeyspaceEvent(0, KeyForServerState(g.ID)))
	c.stateWatchMutex.Unlock()
}

// ReadGIDsForStateEvents returns a pipeline of GIDs for state keyspace events.
func (c *PlayerController) readGIDsForStateEvents(ctx context.Context) <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)

		for {
			switch v := c.stateWatchPS.Receive().(type) {
			case redis.Subscription:
				gid := GIDFromKeyspaceEventTopic(v.Channel)
				ch <- gid
			case redis.Message:
				gid := GIDFromKeyspaceEventTopic(v.Channel)
				ch <- gid
			case error:
				select {
				case <-ctx.Done():
					return
				default:
					log.WithError(v).Error("PlayerController: Couldn't receive state events")
				}
			}
		}
	}()

	return ch
}

// Fulfill ensures that the current state of the given guild matches the desired state.
func (c *PlayerController) Fulfill(gid string) {
	rconn := c.Pool.Get()
	defer rconn.Close()

	state, err := redis.String(rconn.Do("GET", KeyForServerState(gid)))
	if err != nil && err != redis.ErrNil {
		log.WithError(err).WithField("gid", gid).Error("PlayerController: Couldn't get guild state")
		return
	}

	switch state {
	case StateStopped, "":
		log.WithField("gid", gid).Info("PlayerController: State is stopped")
	case StatePlaying:
		log.WithField("gid", gid).Info("PlayerController: State is playing")
	}
}
