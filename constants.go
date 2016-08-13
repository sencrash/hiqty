package main

import (
	"github.com/bwmarrin/discordgo"
)

// Required permissions for the bot to function.
const RequiredPermissions = discordgo.PermissionReadMessages | discordgo.PermissionSendMessages | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak | discordgo.PermissionVoiceUseVAD
