package main

import (
	"encoding/json"
)

type TrackEnvelope struct {
	ServiceID string
	Data      json.RawMessage
}
