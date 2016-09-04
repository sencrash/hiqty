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
	cancels map[string]context.CancelFunc
	mutex   sync.Mutex
	wg      sync.WaitGroup

	stateWatch      Watcher
	stateWatchMutex sync.Mutex
}

// Run runs the player controller. When the context expires, no more players will spawn, and
// existing players will finish playing their current tracks before terminating.
func (c *PlayerController) Run(ctx context.Context) {
	c.redsync = redsync.New([]redsync.Pool{c.Pool})
	c.cancels = make(map[string]context.CancelFunc)

	// Add event handlers.
	defer c.Session.AddHandler(c.HandleGuildCreate)()

	// Watch for keyspace notifications.
	stateWatchConn := c.Pool.Get()
	_, err := stateWatchConn.Do("CONFIG", "SET", "notify-keyspace-events", "AKE")
	if err != nil {
		log.WithError(err).Error("Player: Couldn't enable keyspace events; state watching will not work!")
		return
	}
	c.stateWatch = Watcher{redis.PubSubConn{stateWatchConn}}

	keys := c.stateWatch.Run(ctx)
loop:
	for {
		select {
		case key := <-keys:
			gid := GIDFromKey(key)
			log.WithField("gid", gid).Info("State event")
			c.Fulfill(ctx, gid)
		case <-ctx.Done():
			break loop
		}
	}

	log.Info("PlayerController: Waiting for players to finish...")
	c.wg.Wait()
}

// HandleGuildCreate subscribes to state changes when the bot joins a guild.
func (c *PlayerController) HandleGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	c.stateWatchMutex.Lock()
	c.stateWatch.Subscribe(0, KeyForServerState(g.ID))
	c.stateWatchMutex.Unlock()
}

// HandleGuildDelete unsubscribes from state changes when the bot is kicked from a guild.
func (c *PlayerController) HandleGuildDelete(_ *discordgo.Session, g *discordgo.GuildDelete) {
	c.stateWatchMutex.Lock()
	c.stateWatch.Unsubscribe(0, KeyForServerState(g.ID))
	c.stateWatchMutex.Unlock()
}

// Fulfill ensures that the current state of the given guild matches the desired state.
func (c *PlayerController) Fulfill(ctx context.Context, gid string) {
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

		c.mutex.Lock()
		if cancel := c.cancels[gid]; cancel != nil {
			cancel()
		}
		c.mutex.Unlock()
	case StatePlaying:
		log.WithField("gid", gid).Info("PlayerController: State is playing")

		player := Player{Session: c.Session, Pool: c.Pool, GuildID: gid}
		ctx, cancel := context.WithCancel(ctx)

		c.mutex.Lock()
		c.cancels[gid] = cancel
		c.mutex.Unlock()

		c.wg.Add(1)
		go func() {
			player.Run(ctx)

			c.mutex.Lock()
			delete(c.cancels, gid)
			c.mutex.Unlock()

			c.wg.Done()
		}()
	}
}
