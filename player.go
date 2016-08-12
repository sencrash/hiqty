package main

import (
	"github.com/bwmarrin/discordgo"
	"golang.org/x/net/context"
)

// The Player subsystem watches Redis for tracks to play, and carries out this most important of
// duties. Under no circumstances must the Player communicate directly with a user; that's the
// Responder's job.
type Player struct {
	Session *discordgo.Session
}

// Run runs the player. It will not return until both the context has finished, and there are no
// more tracks currently playing.
func (p *Player) Run(ctx context.Context) {
	// Wait for the context to terminate.
	<-ctx.Done()
}
