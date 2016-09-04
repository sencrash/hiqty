package main

import (
	"context"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"time"
)

type Player struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	GuildID string
}

func (p *Player) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)

loop:
	for {
		<-ticker.C
		log.WithField("gid", p.GuildID).Info("Tick!")

		select {
		case <-ctx.Done():
			break loop
		default:
		}
	}
}
