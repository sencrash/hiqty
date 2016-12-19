package main

import (
	"context"
	"encoding/json"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"github.com/uppfinnarn/hiqty/media"
	"io"
	"net/http"
	"time"
)

// A Player plays music in a server. It watches the playlist and adjusts to changes on its own, but
// watching server state and launching/terminating players is the PlayerController's job.
type Player struct {
	Session *discordgo.Session
	Pool    *redis.Pool
	Client  http.Client

	GuildID string
}

// Run runs the Player. The context expiring will not immediately terminate the player - rather, it
// will terminate after the current song finishes playing.
func (p *Player) Run(ctx context.Context, stop <-chan interface{}) {
	ticker := time.NewTicker(1 * time.Second)

	var cid string
	var voiceState *discordgo.VoiceConnection

	var track media.Track
	var packets <-chan []byte
	var cancel context.CancelFunc

	defer func() {
		if cancel != nil {
			cancel()
		}
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

		if voiceState != nil && voiceState.Ready {
			if track == nil {
				newTrack := p.readFirstTrack()

				if newTrack == nil {
					track = nil
					if cancel != nil {
						cancel()
						cancel = nil
						packets = nil
					}
				} else if !newTrack.Equals(track) {
					if cancel != nil {
						cancel()
						cancel = nil
						packets = nil
					}

					// Note: You can't unmarshal a track with a missing service, so we can safely count
					// on the indicated service's existence at this point.
					svc := media.Services[newTrack.GetServiceID()]

					req, err := svc.BuildMediaRequest(newTrack)
					if err != nil {
						log.WithError(err).WithField("gid", p.GuildID).Error("Player: Couldn't build request")
						continue
					}

					res, err := p.Client.Do(req)
					if err != nil {
						log.WithError(err).WithField("gid", p.GuildID).Error("Player: Couldn't get media source")
						continue
					}

					subctx, c := context.WithCancel(context.Background())
					cancel = c
					packets = p.streamPackets(subctx, p.streamResponse(subctx, res))
					track = newTrack
				}
			}
		}

		select {
		case pkt, ok := <-packets:
			if !ok {
				if cancel != nil {
					cancel()
				}
				track = nil
				continue
			}
			log.WithField("len", len(pkt)).Info("got response packet")
		case <-stop:
			log.WithField("gid", p.GuildID).Info("Stopped")
			break loop
		case <-ctx.Done():
			break loop
		case <-ticker.C:
		}
	}
}

func (p *Player) readFirstTrack() media.Track {
	rconn := p.Pool.Get()
	defer rconn.Close()

	envdatas, err := redis.ByteSlices(rconn.Do("LRANGE", KeyForServerPlaylist(p.GuildID), 0, 1))
	if err != nil {
		log.WithError(err).WithField("gid", p.GuildID).Warn("Player: Couldn't get track")
		return nil
	}
	if len(envdatas) == 0 {
		return nil
	}

	var envelope TrackEnvelope
	if err := json.Unmarshal(envdatas[0], &envelope); err != nil {
		log.WithError(err).WithField("gid", p.GuildID).Error("Player: Invalid envelope encountered!!")
		_, err := rconn.Do("LPOP", KeyForServerPlaylist(p.GuildID))
		if err != nil {
			log.WithField("gid", p.GuildID).WithError(err).Error("Player: Couldn't remove invalid envelope")
		}
		return nil
	}

	return envelope.Track
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

func (p *Player) streamResponse(ctx context.Context, res *http.Response) <-chan []byte {
	ch := make(chan []byte)
	go func() {
		defer res.Body.Close()
		defer close(ch)

		for {
			buf := make([]byte, 1024)
			l, err := res.Body.Read(buf)
			log.WithField("gid", p.GuildID).WithField("l", l).Info("read bytes")
			if err != nil {
				if err != io.EOF {
					log.WithError(err).WithField("gid", p.GuildID).Error("Player: Couldn't read HTTP response")
				}
				return
			}
			ch <- buf[:l]

			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	return ch
}

func (p *Player) streamPackets(ctx context.Context, indata <-chan []byte) <-chan []byte {
	ch := make(chan []byte)
	go func() {
		defer close(ch)

		for {
			select {
			case pkt, ok := <-indata:
				if !ok {
					return
				}
				ch <- pkt
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}
