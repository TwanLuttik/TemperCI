package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const webhookLastMetaKey = "webhook_last"

// WebhookDelivery is the last verified GitHub webhook we accepted.
type WebhookDelivery struct {
	At       time.Time `json:"at"`
	Event    string    `json:"event"`
	Delivery string    `json:"delivery,omitempty"`
}

// RecordWebhookDelivery persists the latest verified GitHub webhook delivery.
func (s *Store) RecordWebhookDelivery(d WebhookDelivery) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store: closed")
	}
	if d.At.IsZero() {
		d.At = time.Now().UTC()
	} else {
		d.At = d.At.UTC()
	}
	d.Event = strings.TrimSpace(d.Event)
	if d.Event == "" {
		d.Event = "unknown"
	}
	d.Delivery = strings.TrimSpace(d.Delivery)
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("store: encode webhook delivery: %w", err)
	}
	_, err = s.db.Exec(`
INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, webhookLastMetaKey, string(raw))
	if err != nil {
		return fmt.Errorf("store: record webhook: %w", err)
	}
	return nil
}

// LastWebhookDelivery returns the most recent verified webhook, or nil.
func (s *Store) LastWebhookDelivery() (*WebhookDelivery, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, webhookLastMetaKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: last webhook: %w", err)
	}
	var d WebhookDelivery
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("store: last webhook parse: %w", err)
	}
	if d.At.IsZero() && d.Event == "" {
		return nil, nil
	}
	return &d, nil
}
