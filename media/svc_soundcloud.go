package media

import (
	"fmt"
)

func init() {
	AddService(SoundCloudID, StandardFactory{
		Env:     []string{"SOUNDCLOUD_CLIENT_ID"},
		NewFunc: NewSoundCloud,
	})
}

var SoundCloudID = "soundcloud"

type SoundCloudTrack struct {
	Title  string
	Author string
	URL    string
}

func (t SoundCloudTrack) Render() string {
	return fmt.Sprintf("**%s**\n*%s*\n<%s>", t.Title, t.Author, t.URL)
}

type SoundCloud struct {
	ClientID string
}

func NewSoundCloud(clientID string) (*SoundCloud, error) {
	return &SoundCloud{
		ClientID: clientID,
	}, nil
}

func (SoundCloud) NewTrack() Track {
	return SoundCloudTrack{}
}
