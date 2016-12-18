package media

import (
	"encoding/json"
	"github.com/pkg/errors"
)

// A ServiceRef is a wrapper around a Service, that (un)marshals services as IDs.
type ServiceRef struct {
	Service Service
}

// MarshalJSON encodes the service as JSON, as an ID string.
func (s ServiceRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Service.ID())
}

// UnmarshalJSON decodes the service from JSON, by looking up the ID in Services.
func (s *ServiceRef) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	svc, ok := Services[id]
	if !ok {
		return errors.New("unknown service: " + id)
	}
	s.Service = svc
	return nil
}

// A Track represents a single track.
type Track struct {
	ID      string
	Service ServiceRef

	Title       string
	Author      string
	Description string
}
