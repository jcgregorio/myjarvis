// Package ha is the Home Assistant REST client. It fetches controllable
// entities, maps their friendly names to entity_ids, and invokes
// services. Tool dispatch is NOT here — see internal/agent for that.
package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Entity is a Home Assistant entity record from /api/states.
type Entity struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// FriendlyName returns Attributes["friendly_name"] or falls back to EntityID.
func (e Entity) FriendlyName() string {
	if name, ok := e.Attributes["friendly_name"].(string); ok && name != "" {
		return name
	}
	return e.EntityID
}

// ToolCall holds the name and raw JSON arguments from an LLM tool call response.
// Lives here (not in internal/llm) so that internal/agent can switch on it
// without llm and agent forming a cycle through ha.Entity.
type ToolCall struct {
	Name string
	Args string
}

// controllableDomains lists HA domains that support turn_on / turn_off.
var controllableDomains = map[string]bool{
	"light":         true,
	"switch":        true,
	"input_boolean": true,
	"fan":           true,
	"cover":         true,
	"media_player":  true,
	"climate":       true,
	"automation":    true,
}

// Client is a thin HA REST client. It is stateless apart from the
// friendly-name → entity_id map populated by FetchControllableEntities.
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	mu       sync.RWMutex
	nameToID map[string]string // lowercased friendly name → entity_id
}

// NewClient builds a Client pointing at the given HA REST base URL with
// the given long-lived access token.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		http:     &http.Client{Timeout: 10 * time.Second},
		nameToID: make(map[string]string),
	}
}

// FetchControllableEntities GETs /api/states, filters to controllable
// domains, and rebuilds the name → entity_id map as a side effect.
func (h *Client) FetchControllableEntities(ctx context.Context) ([]Entity, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", h.baseURL+"/api/states", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA API returned %d", resp.StatusCode)
	}

	var all []Entity
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}

	var result []Entity
	newNames := make(map[string]string)
	for _, e := range all {
		domain, _, ok := strings.Cut(e.EntityID, ".")
		if ok && controllableDomains[domain] {
			result = append(result, e)
			newNames[strings.TrimSpace(strings.ToLower(e.FriendlyName()))] = e.EntityID
		}
	}

	h.SetEntities(newNames)
	return result, nil
}

// LookupEntity returns the entity_id for a friendly name (case-insensitive),
// or ok=false if it isn't known.
func (h *Client) LookupEntity(name string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	normalized := strings.TrimSpace(strings.ToLower(strings.TrimRight(name, " /.,;:!?")))
	id, ok := h.nameToID[normalized]
	if !ok {
		log.Printf("FAILED to find %s in:", normalized)
		for key, value := range h.nameToID {
			log.Printf("%s: %s", key, value)
		}
	}
	return id, ok
}

// SetEntities replaces the friendly-name → entity_id map.
// FetchControllableEntities calls this internally; tests use it to seed
// without a fake HTTP server.
func (h *Client) SetEntities(nameToID map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nameToID = nameToID
}

// CallService POSTs to /api/services/<domain>/<service> with the given
// JSON body. Public so internal/agent can drive it for set_state /
// trigger_automation / set_timer.
func (h *Client) CallService(ctx context.Context, domain, service string, body map[string]any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/services/%s/%s", h.baseURL, domain, service)
	log.Printf("[ha] POST %s %s", url, data)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[ha] response %d: %s", resp.StatusCode, respBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HA service %s/%s returned %d: %s", domain, service, resp.StatusCode, respBody)
	}
	return nil
}
