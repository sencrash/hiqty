package soundcloud

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/uppfinnarn/hiqty/media"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
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
		return []media.Track{s.makeTrack(track)}, nil
	case PlaylistKind:
		var list Playlist
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}

		tracks := make([]media.Track, len(list.Tracks))
		for i, track := range list.Tracks {
			tracks[i] = s.makeTrack(track)
		}
		return tracks, nil
	default:
		return nil, errors.New("unknown envelope type: " + env.Kind)
	}
}

func (s *Service) makeTrack(tr Track) media.Track {
	return media.Track{
		ID:          strconv.FormatInt(tr.ID, 10),
		Service:     media.ServiceRef{s},
		Title:       tr.Title,
		Author:      tr.User.Username,
		Description: tr.Description,
	}
}
