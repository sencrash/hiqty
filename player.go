package main

import (
	"context"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"time"
)

// A Player plays music in a server. It watches the playlist and adjusts to changes on its own, but
// watching server state and launching/terminating players is the PlayerController's job.
type Player struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	GuildID string
}

// Run runs the Player. The context expiring will not immediately terminate the player - rather, it
// will terminate after the current song finishes playing.
func (p *Player) Run(ctx context.Context, stop <-chan interface{}) {
	ticker := time.NewTicker(2 * time.Second)

loop:
	for {
		select {
		case <-ticker.C:
			log.WithField("gid", p.GuildID).Info("Tick!")
		case <-stop:
			log.WithField("gid", p.GuildID).Info("Stopped")
			break loop
		}

		select {
		case <-ctx.Done():
			break loop
		default:
		}
	}
}
