package main

import (
	"context"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
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
	var cid string
	var voiceState *discordgo.VoiceConnection

	defer func() {
		if voiceState != nil {
			if err := voiceState.Disconnect(); err != nil {
				log.WithField("gid", p.GuildID).WithError(err).Error("Player: Couldn't disconnect from voice")
			}
		}
	}()

loop:
	for {
		if cid == "" {
			cid = p.readChannelID()
		}
		if cid != "" && voiceState == nil {
			vs, err := p.Session.ChannelVoiceJoin(p.GuildID, cid, false, false)
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"gid": p.GuildID,
					"cid": cid,
				}).Warn("Player: Couldn't join channel")
				continue
			}
			voiceState = vs
		}
		if cid != "" && voiceState != nil && voiceState.ChannelID != cid {
			if err := voiceState.ChangeChannel(cid, false, false); err != nil {
				log.WithError(err).WithFields(log.Fields{
					"gid": p.GuildID,
					"cid": cid,
				}).Warn("Player: Couldn't change channel")
			}
		}

		select {
		case <-stop:
			log.WithField("gid", p.GuildID).Info("Stopped")
			break loop
		case <-ctx.Done():
			break loop
		}
	}
}

func (p *Player) readChannelID() string {
	rconn := p.Pool.Get()
	defer rconn.Close()

	cid, err := redis.String(rconn.Do("GET", KeyForServerChannel(p.GuildID)))
	if err != nil {
		log.WithError(err).WithField("gid", p.GuildID).Warn("Player: Couldn't get channel")
	}
	return cid
}
