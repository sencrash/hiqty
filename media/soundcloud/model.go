package soundcloud

import (
	"github.com/uppfinnarn/hiqty/media"
)

const (
	UserKind     = "user"
	TrackKind    = "track"
	PlaylistKind = "playlist"
)

type BlankEnvelope struct {
	Kind string `json:"kind"`
}

type User struct {
	ID       int64  `json:"user"`
	Username string `json:"username"`

	PermalinkURL string `json:"permalink_url"`
	AvatarURL    string `json:"avatar_url"`
}

type Track struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	User        User   `json:"user"`

	Streamable bool `json:"streamable"`

	PermalinkURL string `json:"permalink_url"`
	ArtworkURL   string `json:"artwork_url"`
	StreamURL    string `json:"stream_url"`
}

func (t *Track) GetServiceID() string {
	return "soundcloud"
}

func (t Track) GetInfo() media.TrackInfo {
	// Mimic SoundCloud's behavior of showing the user's avatar in lieau of cover art.
	coverURL := t.ArtworkURL
	if coverURL == "" {
		coverURL = t.User.AvatarURL
	}

	return media.TrackInfo{
		Title:       t.Title,
		Description: t.Description,
		URL:         t.PermalinkURL,
		CoverURL:    coverURL,
		User: media.TrackUserInfo{
			Name:      t.User.Username,
			URL:       t.User.PermalinkURL,
			AvatarURL: t.User.AvatarURL,
		},
	}
}

func (t Track) GetPlayable() (bool, string) {
	if !t.Streamable {
		return false, "The artist has disabled streaming for this track."
	}
	return true, ""
}

func (t Track) Equals(other media.Track) bool {
	if other == nil {
		return false
	}
	t2, ok := other.(*Track)
	return ok && t.ID == t2.ID
}

type Playlist struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Tracks      []Track `json:"tracks"`
	User        User    `json:"user"`
}
