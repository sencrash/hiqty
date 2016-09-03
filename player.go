package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"gopkg.in/redsync.v1"
	"io"
	"os/exec"
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
	if len(p.vcCancels) > 0 {
		log.WithField("num", len(p.vcCancels)).Info("Player: Waiting for tracks to finish...")
	}
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

		go func() {
			p.vcWaitGroup.Add(1)

			p.Play(ctx, g, mutex)
			mutex.Unlock()

			p.vcMutex.Lock()
			delete(p.vcCancels, g.ID)
			p.vcMutex.Unlock()

			p.vcWaitGroup.Done()
		}()
	}
}

func (p *Player) Play(ctx context.Context, g *discordgo.Guild, mutex *redsync.Mutex) {
	extendTicker := time.NewTicker(10 * time.Second)

	rconn := p.Pool.Get()
	defer rconn.Close()

	cid, err := redis.String(rconn.Do("GET", KeyForServerChannel(g.ID)))
	if err != nil {
		log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Couldn't get voice channel ID")
		return
	}

	vc, err := p.Session.ChannelVoiceJoin(g.ID, cid, false, false)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"gid": g.ID, "cid": cid}).Error("Player: Play: Couldn't join channel")
		return
	}
	defer func() {
		log.WithField("gid", g.ID).Info("Player: Play: Voice Disconnecting")
		vc.Disconnect()
	}()

	playError := make(chan error, 1)
	for {
		var currentTrack Track
		for {
			bytes, err := redis.ByteSlices(rconn.Do("LRANGE", KeyForServerPlaylist(g.ID), 0, 1))
			if err != nil {
				log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Couldn't get last track!")
				return
			}
			if len(bytes) == 0 {
				log.WithField("gid", g.ID).Info("Player: Play: Playlist is empty!")
				if _, err := rconn.Do("DEL", KeyForServerState(g.ID)); err != nil {
					log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Couldn't delete state marker")
				}
				return
			}
			if err := json.Unmarshal(bytes[0], &currentTrack); err != nil {
				log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Discarding malformed track")
				if _, err := rconn.Do("LPOP", KeyForServerPlaylist(g.ID)); err != nil {
					log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Couldn't pop malformed track")
				}
				continue
			}
			break
		}

		log.WithField("title", currentTrack.Title).Info("Decoded track")

		go func() { playError <- p.PlayTrack(ctx, vc, currentTrack) }()
	innerLoop:
		for {
			select {
			case err := <-playError:
				if err != nil {
					log.WithError(err).WithFields(log.Fields{
						"gid":   g.ID,
						"cid":   cid,
						"title": currentTrack.Title,
						"url":   currentTrack.URL,
					}).Error("Player: Play: Playback error")
				}
				if _, err := rconn.Do("LPOP", KeyForServerPlaylist(g.ID)); err != nil {
					log.WithError(err).WithField("gid", g.ID).Error("Player: Play: Couldn't pop completed track")
				}
				log.WithField("gid", g.ID).Info("Track done")
				break innerLoop
			case <-ctx.Done():
				return
			case <-extendTicker.C:
				if !mutex.Extend() {
					log.WithField("gid", g.ID).Error("Player: Play: Failed to extend mutex!")
					return
				}
			}
		}
	}
}

// PlayTrack plays a music track on a voice channel.
// TODO: Don't subprocess ytdl! It's only there to test that the rest works before it's replaced.
// Large chunks of this are straight-up copypaste from iopred's bruxism; this, too, is temporary.
func (p *Player) PlayTrack(ctx context.Context, vc *discordgo.VoiceConnection, t Track) error {
	ytdl := exec.Command("youtube-dl", "-v", "-f", "bestaudio", "-o", "-", t.URL)
	ytdlout, err := ytdl.StdoutPipe()
	if err != nil {
		return err
	}
	ytdlbuf := bufio.NewReaderSize(ytdlout, 16384)

	ffmpeg := exec.Command("ffmpeg", "-i", "pipe:0", "-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
	ffmpeg.Stdin = ytdlbuf
	ffmpegout, err := ffmpeg.StdoutPipe()
	if err != nil {
		return err
	}
	ffmpegbuf := bufio.NewReaderSize(ffmpegout, 16384)

	dca := exec.Command("dca", "-raw", "-i", "pipe:0")
	dca.Stdin = ffmpegbuf
	dcaout, err := dca.StdoutPipe()
	if err != nil {
		return err
	}
	dcabuf := bufio.NewReaderSize(dcaout, 16384)

	err = ytdl.Start()
	if err != nil {
		return err
	}
	defer func() {
		go ytdl.Wait()
	}()

	err = ffmpeg.Start()
	if err != nil {
		return err
	}
	defer func() {
		go ffmpeg.Wait()
	}()

	err = dca.Start()
	if err != nil {
		return err
	}
	defer func() {
		go dca.Wait()
	}()

	log.Info("Speaking up")
	if err := vc.Speaking(true); err != nil {
		return err
	}
	defer func() {
		log.Info("Shutting up")
		vc.Speaking(false)
	}()

	time.Sleep(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var length int16
		if err := binary.Read(dcabuf, binary.LittleEndian, &length); err != nil {
			// An EOF just means the song is done, nothing strange here
			if err == io.EOF {
				break
			}
			return err
		}

		pkt := make([]byte, length)
		if err := binary.Read(dcabuf, binary.LittleEndian, &pkt); err != nil {
			return err
		}
		vc.OpusSend <- pkt
	}

	return nil
}
