package control

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Hub broadcasts realtime dashboard snapshots to WebSocket clients.
type Hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
	log     *slog.Logger
	up      websocket.Upgrader
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// NewHub creates a dashboard event hub.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		clients: make(map[*wsClient]struct{}),
		log:     log,
		up: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true }, // same-origin SPA; cookies handle auth
		},
	}
}

// BroadcastJSON sends a typed message to all connected clients.
func (h *Hub) BroadcastJSON(v any) {
	if h == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- raw:
		default:
			// Slow client: drop this message.
		}
	}
}

// ServeWS upgrades to WebSocket and pumps messages.
// If initial is non-nil it is sent first (typically a snapshot JSON).
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, initial []byte) {
	if h == nil {
		http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("websocket upgrade failed", "err", err)
		return
	}
	c := &wsClient{conn: conn, send: make(chan []byte, 16)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	go c.writePump()
	if len(initial) > 0 {
		select {
		case c.send <- initial:
		default:
		}
	} else {
		select {
		case c.send <- []byte(`{"type":"hello"}`):
		default:
		}
	}
	c.readPump(func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		close(c.send)
		_ = conn.Close()
	})
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) readPump(onClose func()) {
	defer onClose()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// ClientCount returns connected dashboard sockets.
func (h *Hub) ClientCount() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
