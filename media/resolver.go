package media

type TrackEnvelope struct {
	Service string
	Track   Track
}

type Track interface {
	Render() string
}

type Service interface {
	NewTrack() Track
}

type Resolver struct {
	Backends map[string]Service
}

func NewResolver() *Resolver {
	return &Resolver{
		Backends: make(map[string]Service),
	}
}
