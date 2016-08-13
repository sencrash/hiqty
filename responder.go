package main

import (
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/garyburd/redigo/redis"
	"github.com/mvdan/xurls"
	"golang.org/x/net/context"
	"strings"
)

// The Responder subsystem responds to user commands in chat rooms, and dispatches commands. It's
// important to note that the Responder has no direct access to the Player, nor should it - all
// communication is to be done through a central message bus.
type Responder struct {
	Session *discordgo.Session
	Pool    *redis.Pool

	mentionByUsername string // <@USER_SNOWFLAKE_ID>
	mentionByNickname string // <@!USER_SNOWFLAKE_ID>
}

// Run runs the responder. When the context is terminated, cleanly detach from the session to allow
// it to outlive the responder - there may still be unfinished songs playing.
func (r *Responder) Run(ctx context.Context) {
	// Registering a handler returns a function that unregisters it.
	deregReady := r.Session.AddHandler(r.HandleReady)
	defer deregReady()
	deregMessageCreate := r.Session.AddHandler(r.HandleMessageCreate)
	defer deregMessageCreate()

	// Wait for the context to terminate.
	<-ctx.Done()
}

// HandleReady handles the ready event.
func (r *Responder) HandleReady(_ *discordgo.Session, e *discordgo.Ready) {
	// Figure out what mentions of the bot look like, so we can just compare prefixes later.
	r.mentionByUsername = fmt.Sprintf("<@%s>", e.User.ID)
	r.mentionByNickname = fmt.Sprintf("<@!%s>", e.User.ID)
}

// HandleMessageCreate handles incoming messages.
func (r *Responder) HandleMessageCreate(_ *discordgo.Session, msg *discordgo.MessageCreate) {
	// Having to make a REST call for the channel info should be an exceedingly rare case, but it
	// is technically possible to receive messages before guild info is sent out.
	channel, err := r.Session.State.Channel(msg.ChannelID)
	if err != nil {
		channel, err = r.Session.Channel(msg.ChannelID)
		if err != nil {
			log.WithError(err).Error("Couldn't get channel info")
			return
		}
	}

	// Private calls can't have bots in them (yet?), as they're closely tied to the friend system,
	// and bots can't have friends :<
	// TODO: Reply to DMs with a big help blurb! It just needs to be written first...
	if channel.IsPrivate {
		return
	}

	// If it's public, we only care about mentions!
	if !strings.HasPrefix(msg.Content, r.mentionByUsername) && !strings.HasPrefix(msg.Content, r.mentionByNickname) {
		return
	}

	// Find all URLs in the message.
	urls := xurls.Strict.FindAllString(msg.Content, -1)
	for _, url := range urls {
		log.WithField("url", url).Info("Looks like a URL!")
	}
}
