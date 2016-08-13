package main

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
)

// Required permissions for the bot to function.
const RequiredPermissions = discordgo.PermissionReadMessages | discordgo.PermissionSendMessages | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak | discordgo.PermissionVoiceUseVAD

// KeyForServer returns the redis key for the server's given subkey.
func KeyForServer(gid, key string) string { return fmt.Sprintf("hiqty:server:%s:%s", gid, key) }

// KeyForServerPlaylist returns the redis key for a server's playlist.
func KeyForServerPlaylist(gid string) string { return KeyForServer(gid, "playlist") }

// KeyForServerState returns the redis key for a server's state.
func KeyForServerState(gid string) string { return KeyForServer(gid, "state") }

// KeyForServerPlayerLock returns the redis key for a server's player lock.
func KeyForServerPlayerLock(gid string) string { return KeyForServer(gid, "player_lock") }

// TopicForKeyspaceEvent returns the topic for keyspace events on the given key.
func TopicForKeyspaceEvent(db int, key string) string {
	return fmt.Sprintf("__keyspace@%d__:%s", db, key)
}
