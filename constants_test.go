package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestTopicForKeyspaceEvent(t *testing.T) {
	assert.Equal(t, "__keyspace@0__:some:key", TopicForKeyspaceEvent(0, "some:key"))
}

func TestKeyFromKeyspaceTopic(t *testing.T) {
	assert.Equal(t, "some:key", KeyFromKeyspaceTopic("__keyspace@0__:some:key"))
}
