package media

import (
	"net/url"
)

// Global registry of available services.
var Services = make(map[string]Service)

// Registers a service with the registry.
func Register(svc Service) {
	Services[svc.ID()] = svc
}

// A Service facilitates communication with a streaming service of some kind.
type Service interface {
	// An arbitrary ID for the service, used for track serialization.
	ID() string

	// Attribution info for the service.
	Attribution() ServiceAttribution

	// Return true if the URL looks interesting.
	Sniff(u *url.URL) bool

	// Resolve is called if Sniff() returns true, and resolves a URL into one or more tracks.
	Resolve(u *url.URL) ([]Track, error)

	// Returns a blank track. Used to unmarshal tracks from envelopes.
	NewTrack() Track
}
