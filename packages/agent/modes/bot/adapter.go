// Package bot provides a protocol-agnostic runner for long-running bot
// modes.  Concrete transports (Telegram, Discord, …) implement the
// BotAdapter interface; the Runner handles turn queueing, agent
// prompting, command dispatch, and credential refresh.
package bot

import (
	"context"

	"github.com/bnema/zut/packages/provider"
)

// InboundMessage is a protocol-normalised message from a user.
type InboundMessage struct {
	ChannelID string // opaque; adapter owns encoding (e.g. fmt.Sprintf("%d", chatID))
	MessageID string // optional reply anchor
	Text      string
	Images    []provider.ImageBlock
}

// Command is a built-in bot command that bypasses the agent.
type Command int

const (
	CmdStart  Command = iota // first-time pairing / welcome
	CmdHelp                  // usage information
	CmdStatus                // agent/provider state
	CmdStop                  // cancel the active turn
)

// SendOptions carries protocol-agnostic delivery hints for outbound messages.
type SendOptions struct {
	// ReplyToMessageID is an optional adapter-owned message identifier to anchor replies.
	ReplyToMessageID string
}

// BotAdapter is the transport layer a concrete protocol must implement.
// The Runner calls these methods; it never touches protocol types directly.
type BotAdapter interface {
	// Run drives inbound polling; calls handler for normal messages and
	// commandHandler for built-in commands.  Blocks until ctx is done.
	Run(ctx context.Context,
		handler func(InboundMessage),
		commandHandler func(Command, InboundMessage),
	) error

	// Send delivers a reply.  The adapter chunks to protocol limits.
	Send(ctx context.Context, channelID, text string, opts SendOptions) error

	// IndicateWorking fires a "typing…" signal; returns a stop func.
	// Return a no-op if the protocol doesn't support it.
	IndicateWorking(ctx context.Context, channelID string) (stop func())

	// StatusText appends protocol-specific info to /status replies
	// (e.g. "@botname").  Return "" if there is nothing to add.
	StatusText() string
}
