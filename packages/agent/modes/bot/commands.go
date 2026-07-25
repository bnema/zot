package bot

import "strings"

// ParseCommand recognizes the built-in slash commands shared by messaging
// adapters. Command names are case-insensitive, and trailing arguments do not
// affect recognition.
func ParseCommand(text string) (Command, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, false
	}
	switch strings.ToLower(fields[0]) {
	case "/start":
		return CmdStart, true
	case "/help":
		return CmdHelp, true
	case "/status":
		return CmdStatus, true
	case "/stop":
		return CmdStop, true
	default:
		return 0, false
	}
}

// IsStopCommand reports whether text should abort the active turn.
// Users often type plain "stop" rather than bot-style "/stop"; keep
// this intentionally narrow so normal prompts like "stop doing X"
// still go to the agent.
func IsStopCommand(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "stop")
}
