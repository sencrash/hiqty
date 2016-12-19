package main

import (
	"encoding/json"
	"github.com/pkg/errors"
	"github.com/uppfinnarn/hiqty/media"
)

type TrackEnvelope struct {
	ServiceID string
	Track     media.Track
}

func (e *TrackEnvelope) UnmarshalJSON(data []byte) error {
	var tmp struct {
		ServiceID string
		Track     json.RawMessage
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	svc := media.Services[tmp.ServiceID]
	if svc == nil {
		return errors.New("unknown service: " + tmp.ServiceID)
	}

	track := svc.NewTrack()
	if err := json.Unmarshal(tmp.Track, track); err != nil {
		return err
	}

	e.ServiceID = tmp.ServiceID
	e.Track = track

	return nil
}
