package soundcloud

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
}

type Track struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	User        User   `json:"user"`
}

type Playlist struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Tracks      []Track `json:"tracks"`
	User        User    `json:"user"`
}
