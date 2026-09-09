package ws

import (
	"encoding/json"
	"sync"
)

// Event names following Chatwoot standards
const (
	EventMessageCreated      = "message.created"
	EventMessageUpdated      = "message.updated"
	EventMessageDeleted      = "message.deleted"
	EventConversationUpdated  = "conversation.updated"
	EventConversationStatus   = "conversation.status_changed"
	EventConversationAssigned = "conversation.assigned"
	EventPresenceUpdate       = "presence.update"
)

type Event struct {
	Name           string `json:"event"`
	AccountID      uint   `json:"account_id"`
	ConversationID uint   `json:"conversation_id,omitempty"`
	Data           any    `json:"data"`
}

type Hub struct {
	clientsMu sync.RWMutex
	// Map of Client -> true
	clients map[*Client]bool

	register   chan *Client
	unregister chan *Client
	broadcast  chan *Event
}

var defaultHub *Hub
var once sync.Once

func GetHub() *Hub {
	once.Do(func() {
		defaultHub = NewHub()
		go defaultHub.Run()
	})
	return defaultHub
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *Event, 256),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clientsMu.Lock()
			h.clients[client] = true
			h.clientsMu.Unlock()

		case client := <-h.unregister:
			h.clientsMu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.clientsMu.Unlock()

		case event := <-h.broadcast:
			h.clientsMu.RLock()
			msgBytes, err := json.Marshal(event)
			if err != nil {
				h.clientsMu.RUnlock()
				continue
			}

			for client := range h.clients {
				// Filter based on client type
				if client.IsAgent {
					// Agent receives all events for their account
					if client.AccountID == event.AccountID {
						select {
						case client.send <- msgBytes:
						default:
							// Buffer full, drop or close
						}
					}
				} else {
					// Visitor only receives events for their conversation
					if event.ConversationID > 0 && client.ConversationID == event.ConversationID {
						select {
						case client.send <- msgBytes:
						default:
						}
					}
				}
			}
			h.clientsMu.RUnlock()
		}
	}
}

func (h *Hub) Broadcast(event *Event) {
	h.broadcast <- event
}

func (h *Hub) ClientCount() int {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()
	return len(h.clients)
}
