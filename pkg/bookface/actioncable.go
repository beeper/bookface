package bookface

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/rs/zerolog"
)

const (
	ActionCableURL      = "wss://messages.ycombinator.com/cable"
	HeartbeatInterval   = 10 * time.Second
	ReconnectBaseDelay  = 1 * time.Second
	ReconnectMaxDelay   = 60 * time.Second
	ReconnectMultiplier = 2.0
)

// ActionCable protocol message types
const (
	ActionCableWelcome             = "welcome"
	ActionCablePing                = "ping"
	ActionCableConfirmSubscription = "confirm_subscription"
	ActionCableRejectSubscription  = "reject_subscription"
	ActionCableDisconnect          = "disconnect"
)

// ActionCable command types
const (
	ActionCableCommandSubscribe   = "subscribe"
	ActionCableCommandUnsubscribe = "unsubscribe"
	ActionCableCommandMessage     = "message"
)

// ActionCableState represents the connection state.
type ActionCableState int32

const (
	StateDisconnected ActionCableState = iota
	StateConnecting
	StateConnected
	StateSubscribed
)

// ServerMessage represents an incoming ActionCable message from the server.
type ServerMessage struct {
	Type       string          `json:"type,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`
	Identifier string          `json:"identifier,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Reconnect  bool            `json:"reconnect,omitempty"`
}

// ClientCommand represents an outgoing ActionCable command to the server.
type ClientCommand struct {
	Command    string `json:"command"`
	Identifier string `json:"identifier"`
	Data       string `json:"data,omitempty"`
}

// ChannelIdentifier represents the ActionCable channel subscription identifier.
type ChannelIdentifier struct {
	Channel string `json:"channel"`
	Room    string `json:"room"`
}

// ChatChannelMessage represents a message received on the ChatChannel.
type ChatChannelMessage struct {
	ChatID           int64           `json:"chat_id"`
	Type             string          `json:"type"`
	Message          json.RawMessage `json:"message,omitempty"`
	UserID           int64           `json:"user_id,omitempty"`
	LastActiveOnChat time.Time       `json:"last_active_on_chat_at,omitzero"`
}

// ActionCableEvent is emitted for real-time events from the WebSocket.
type ActionCableEvent struct {
	ChatID           int64
	Type             string // "chat_message_updated", "user_last_active_on_chat_at"
	Message          *Message
	UserID           int64
	LastActiveOnChat time.Time
}

// ActionCableHandler is the callback type for received events.
type ActionCableHandler func(event *ActionCableEvent)

// ActionCable manages the WebSocket connection to the Bookface ActionCable server.
type ActionCable struct {
	jar    *StringCookieJar
	userID int64
	log    zerolog.Logger

	conn  *websocket.Conn
	state atomic.Int32

	handler ActionCableHandler

	// Lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Reconnection state
	reconnectDelay time.Duration

	// Subscription identifier (cached for heartbeat)
	subscriptionIdentifier string
}

// NewActionCable creates a new ActionCable connection manager.
func NewActionCable(jar *StringCookieJar, userID int64, handler ActionCableHandler, log zerolog.Logger) *ActionCable {
	return &ActionCable{
		jar:            jar,
		userID:         userID,
		handler:        handler,
		log:            log.With().Str("component", "actioncable").Logger(),
		reconnectDelay: ReconnectBaseDelay,
	}
}

// Start initiates the WebSocket connection.
func (ac *ActionCable) Start(ctx context.Context) error {
	ac.ctx, ac.cancel = context.WithCancel(ctx)

	ac.wg.Add(1)
	go ac.connectionLoop()

	return nil
}

// Stop gracefully closes the WebSocket connection.
func (ac *ActionCable) Stop() {
	if ac.cancel != nil {
		ac.cancel()
	}
	ac.wg.Wait()
	ac.state.Store(int32(StateDisconnected))
}

// connectionLoop handles connection, reconnection, and message processing.
func (ac *ActionCable) connectionLoop() {
	defer ac.wg.Done()

	for {
		select {
		case <-ac.ctx.Done():
			ac.closeConnection()
			return
		default:
		}

		err := ac.connect()
		if err != nil {
			ac.log.Warn().Err(err).Dur("retry_in", ac.reconnectDelay).Msg("Connection failed, will retry")
			ac.waitForReconnect()
			continue
		}

		// Reset reconnect delay on successful connection
		ac.reconnectDelay = ReconnectBaseDelay

		// Run message loop (blocks until disconnection)
		ac.messageLoop()

		// If context is cancelled, exit
		select {
		case <-ac.ctx.Done():
			return
		default:
			ac.log.Info().Msg("Disconnected, will reconnect")
			ac.waitForReconnect()
		}
	}
}

// connect establishes the WebSocket connection.
func (ac *ActionCable) connect() error {
	ac.state.Store(int32(StateConnecting))
	ac.log.Debug().Msg("Connecting to ActionCable")

	header := http.Header{}
	header.Set("Origin", "https://messages.ycombinator.com")
	header.Set("User-Agent", UserAgent)

	conn, _, err := websocket.Dial(ac.ctx, ActionCableURL, &websocket.DialOptions{
		HTTPClient:   &http.Client{Jar: ac.jar},
		HTTPHeader:   header,
		Subprotocols: []string{"actioncable-v1-json", "actioncable-unsupported"},
	})
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}

	ac.conn = conn

	// Wait for welcome message
	if err := ac.waitForWelcome(); err != nil {
		ac.closeConnection()
		return fmt.Errorf("failed to receive welcome: %w", err)
	}

	ac.state.Store(int32(StateConnected))
	ac.log.Info().Msg("Connected to ActionCable")

	// Subscribe to user's chat channel
	if err := ac.subscribe(); err != nil {
		ac.closeConnection()
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	return nil
}

// waitForWelcome waits for the initial welcome message.
func (ac *ActionCable) waitForWelcome() error {
	ctx, cancel := context.WithTimeout(ac.ctx, 10*time.Second)
	defer cancel()

	var msg ServerMessage
	if err := wsjson.Read(ctx, ac.conn, &msg); err != nil {
		return err
	}

	if msg.Type != ActionCableWelcome {
		return fmt.Errorf("expected welcome, got %s", msg.Type)
	}

	return nil
}

// subscribe sends the subscription command for the user's ChatChannel.
func (ac *ActionCable) subscribe() error {
	identifier := ChannelIdentifier{
		Channel: "ChatChannel",
		Room:    fmt.Sprintf("chat_channel_user_%d", ac.userID),
	}

	identJSON, _ := json.Marshal(identifier)
	ac.subscriptionIdentifier = string(identJSON)

	cmd := ClientCommand{
		Command:    ActionCableCommandSubscribe,
		Identifier: ac.subscriptionIdentifier,
	}

	if err := wsjson.Write(ac.ctx, ac.conn, cmd); err != nil {
		return fmt.Errorf("failed to send subscribe: %w", err)
	}

	// Wait for confirmation
	ctx, cancel := context.WithTimeout(ac.ctx, 10*time.Second)
	defer cancel()

	var msg ServerMessage
	if err := wsjson.Read(ctx, ac.conn, &msg); err != nil {
		return err
	}

	if msg.Type == ActionCableRejectSubscription {
		return fmt.Errorf("subscription rejected: %s", msg.Reason)
	}

	if msg.Type != ActionCableConfirmSubscription {
		return fmt.Errorf("expected confirm_subscription, got %s", msg.Type)
	}

	ac.state.Store(int32(StateSubscribed))
	ac.log.Info().Str("room", identifier.Room).Msg("Subscribed to ChatChannel")

	// Start heartbeat goroutine
	ac.wg.Add(1)
	go ac.heartbeatLoop()

	return nil
}

// messageLoop processes incoming WebSocket messages.
func (ac *ActionCable) messageLoop() {
	for {
		select {
		case <-ac.ctx.Done():
			return
		default:
		}

		var msg ServerMessage
		err := wsjson.Read(ac.ctx, ac.conn, &msg)
		if err != nil {
			if ac.ctx.Err() != nil {
				return // Context cancelled, clean exit
			}
			ac.log.Warn().Err(err).Msg("Error reading message")
			return // Will trigger reconnect
		}

		ac.handleMessage(&msg)
	}
}

// handleMessage processes a single incoming message.
func (ac *ActionCable) handleMessage(msg *ServerMessage) {
	switch msg.Type {
	case ActionCablePing:
		ac.log.Trace().Msg("Received ping")

	case ActionCableDisconnect:
		ac.log.Warn().Str("reason", msg.Reason).Bool("reconnect", msg.Reconnect).Msg("Server requested disconnect")
		ac.closeConnection()

	case "": // Data message (no type field means it's a channel message)
		if msg.Identifier != "" && len(msg.Message) > 0 {
			ac.handleDataMessage(msg)
		}

	default:
		ac.log.Debug().Str("type", msg.Type).Msg("Unhandled message type")
	}
}

// handleDataMessage processes chat channel data messages.
func (ac *ActionCable) handleDataMessage(msg *ServerMessage) {
	var channelMsg ChatChannelMessage
	if err := json.Unmarshal(msg.Message, &channelMsg); err != nil {
		ac.log.Warn().Err(err).Msg("Failed to parse channel message")
		return
	}

	ac.log.Debug().
		Int64("chat_id", channelMsg.ChatID).
		Str("event_type", channelMsg.Type).
		Msg("Received channel message")

	event := &ActionCableEvent{
		ChatID: channelMsg.ChatID,
		Type:   channelMsg.Type,
	}

	switch channelMsg.Type {
	case "chat_message_updated":
		var message Message
		if err := json.Unmarshal(channelMsg.Message, &message); err != nil {
			ac.log.Warn().Err(err).Msg("Failed to parse chat_message_updated")
			return
		}
		event.Message = &message

	case "user_last_active_on_chat_at":
		event.UserID = channelMsg.UserID
		event.LastActiveOnChat = channelMsg.LastActiveOnChat

	default:
		ac.log.Debug().Str("type", channelMsg.Type).Msg("Unhandled channel message type")
		return
	}

	// Dispatch to handler
	if ac.handler != nil {
		ac.handler(event)
	}
}

// heartbeatLoop sends periodic heartbeat messages.
func (ac *ActionCable) heartbeatLoop() {
	defer ac.wg.Done()

	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ac.ctx.Done():
			return
		case <-ticker.C:
			if ac.state.Load() != int32(StateSubscribed) {
				return
			}

			data, _ := json.Marshal(map[string]string{"action": "heartbeat"})
			cmd := ClientCommand{
				Command:    ActionCableCommandMessage,
				Identifier: ac.subscriptionIdentifier,
				Data:       string(data),
			}

			if err := wsjson.Write(ac.ctx, ac.conn, cmd); err != nil {
				ac.log.Warn().Err(err).Msg("Failed to send heartbeat")
				return
			}

			ac.log.Trace().Msg("Sent heartbeat")
		}
	}
}

// closeConnection cleanly closes the WebSocket.
func (ac *ActionCable) closeConnection() {
	if ac.conn != nil {
		ac.conn.Close(websocket.StatusNormalClosure, "closing")
		ac.conn = nil
	}
	ac.state.Store(int32(StateDisconnected))
}

// waitForReconnect implements exponential backoff.
func (ac *ActionCable) waitForReconnect() {
	select {
	case <-ac.ctx.Done():
		return
	case <-time.After(ac.reconnectDelay):
	}

	// Increase delay for next time (exponential backoff)
	ac.reconnectDelay = min(time.Duration(float64(ac.reconnectDelay)*ReconnectMultiplier), ReconnectMaxDelay)
}

// GetState returns the current connection state.
func (ac *ActionCable) GetState() ActionCableState {
	return ActionCableState(ac.state.Load())
}
