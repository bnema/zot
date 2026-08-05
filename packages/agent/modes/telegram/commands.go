package telegram

import "github.com/bnema/zut/packages/agent/modes/bot"

// isStopCommand is a shim to bot.IsStopCommand for backward compatibility.
var isStopCommand = bot.IsStopCommand
