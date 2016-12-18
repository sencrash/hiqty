package soundcloud

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/uppfinnarn/hiqty/media"
	"io/ioutil"
	"net/http"
	"net/url"
)

type Service struct {
	Client   http.Client
	ClientID string
}

func New(clientID string) *Service {
	return &Service{
		ClientID: clientID,
	}
}

func (s *Service) ID() string {
	return "soundcloud"
}

func (s *Service) Attribution() media.ServiceAttribution {
	// TODO: Ask SoundCloud if it's okay to use an orange logo!
	// They explicitly ask for it to be black or white, but we can't tell which one will actually
	// be visible with the user's chosen theme.
	return media.ServiceAttribution{
		Text:    "Powered by SoundCloud",
		LogoURL: "https://w.soundcloud.com/icon/assets/images/orange_transparent_64-94fc761.png",
	}
}

func (s *Service) Sniff(u *url.URL) bool {
	return (u.Host == "soundcloud.com")
}

func (s *Service) Resolve(u *url.URL) ([]media.Track, error) {
	apiURL := fmt.Sprintf("https://api.soundcloud.com/resolve?client_id=%s&url=%s", s.ClientID, url.QueryEscape(u.String()))
	res, err := s.Client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	data, _ := ioutil.ReadAll(res.Body)
	res.Body.Close()

	var env BlankEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}

	switch env.Kind {
	case TrackKind:
		var track Track
		if err := json.Unmarshal(data, &track); err != nil {
			return nil, err
		}
		return []media.Track{media.Track(track)}, nil
	case PlaylistKind:
		var list Playlist
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}

		tracks := make([]media.Track, len(list.Tracks))
		for i, track := range list.Tracks {
			tracks[i] = media.Track(track)
		}
		return tracks, nil
	default:
		return nil, errors.New("unknown envelope type: " + env.Kind)
	}
}

func (s *Service) NewTrack() media.Track {
	return Track{}
}

func (s *Service) BuildMediaRequest(t_ media.Track) (*http.Request, error) {
	t := t_.(Track)
	return http.NewRequest("GET", t.StreamURL+"?client_id="+s.ClientID, nil)
}
