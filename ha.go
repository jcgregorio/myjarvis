package main

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

type HAClient struct {
	baseURL  string
	token    string
	http     *http.Client
	mu       sync.RWMutex
	nameToID map[string]string // friendly name → entity_id
	vault    *VaultSearcher
}

func NewHAClient(baseURL, token string) *HAClient {
	return &HAClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		http:     &http.Client{Timeout: 10 * time.Second},
		nameToID: make(map[string]string),
	}
}

type HAEntity struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func (e HAEntity) FriendlyName() string {
	if name, ok := e.Attributes["friendly_name"].(string); ok && name != "" {
		return name
	}
	return e.EntityID
}

// controllableDomains lists HA domains that support turn_on / turn_off.
var controllableDomains = map[string]bool{
	"light":        true,
	"switch":       true,
	"input_boolean": true,
	"fan":          true,
	"cover":        true,
	"media_player": true,
	"climate":      true,
	"script":       true,
	"automation":   true,
}

func (h *HAClient) FetchControllableEntities(ctx context.Context) ([]HAEntity, error) {
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

	var all []HAEntity
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}

	var result []HAEntity
	newNames := make(map[string]string)
	for _, e := range all {
		domain, _, ok := strings.Cut(e.EntityID, ".")
		if ok && controllableDomains[domain] {
			result = append(result, e)
			newNames[strings.ToLower(e.FriendlyName())] = e.EntityID
		}
	}

	h.mu.Lock()
	h.nameToID = newNames
	h.mu.Unlock()

	return result, nil
}

// ToolCall holds the name and raw JSON arguments from an LLM tool call response.
type ToolCall struct {
	Name string
	Args string
}

// ExecuteToolCall dispatches a tool call. For vault tools (search_notes,
// summarize_notes) it needs a VaultSearcher, which is set via SetVault.
func (h *HAClient) ExecuteToolCall(ctx context.Context, tc ToolCall) (string, error) {
	switch tc.Name {
	case "set_state":
		return "", h.executeSetState(ctx, tc.Args)
	case "trigger_automation":
		return "", h.executeTriggerAutomation(ctx, tc.Args)
	case "set_timer":
		return "", h.executeSetTimer(ctx, tc.Args)
	case "check_list":
		return h.executeCheckList(ctx, tc.Args)
	case "check_off_item":
		return "", h.executeCheckOffItem(ctx, tc.Args)
	case "uncheck_item":
		return "", h.executeUncheckItem(ctx, tc.Args)
	case "clean_lists":
		return "", CleanLists()
	case "add_to_list":
		return "", h.executeAddToList(ctx, tc.Args)
	case "search_notes":
		if h.vault == nil {
			return "", fmt.Errorf("vault not configured")
		}
		return h.vault.SearchNotes(ctx, tc.Args)
	case "summarize_notes":
		if h.vault == nil {
			return "", fmt.Errorf("vault not configured")
		}
		return h.vault.SummarizeNotes(ctx, tc.Args)
	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}
}

func (h *HAClient) SetVault(v *VaultSearcher) {
	h.vault = v
}

func (h *HAClient) executeSetState(ctx context.Context, args string) error {
	var p struct {
		Entity string `json:"entity"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	if p.State != "on" && p.State != "off" {
		return fmt.Errorf("invalid state %q: must be on or off", p.State)
	}
	h.mu.RLock()
	entityID, ok := h.nameToID[strings.ToLower(p.Entity)]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown entity %q", p.Entity)
	}
	domain, _, _ := strings.Cut(entityID, ".")
	return h.callService(ctx, domain, "turn_"+p.State, map[string]any{"entity_id": entityID})
}

func (h *HAClient) executeTriggerAutomation(ctx context.Context, args string) error {
	var p struct {
		Entity string `json:"entity"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	h.mu.RLock()
	entityID, ok := h.nameToID[strings.ToLower(p.Entity)]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown automation %q", p.Entity)
	}
	domain, _, _ := strings.Cut(entityID, ".")
	service := "trigger"
	if domain == "script" {
		service = "turn_on"
	}
	return h.callService(ctx, domain, service, map[string]any{"entity_id": entityID})
}

func (h *HAClient) executeSetTimer(ctx context.Context, args string) error {
	var p struct {
		Name            string `json:"name"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	hours := p.DurationSeconds / 3600
	minutes := (p.DurationSeconds % 3600) / 60
	seconds := p.DurationSeconds % 60
	return h.callService(ctx, "timer", "start", map[string]any{
		"entity_id": "timer." + sanitizeName(p.Name),
		"duration":  fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds),
	})
}

func (h *HAClient) executeCheckList(_ context.Context, args string) (string, error) {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	contents, err := ReadList(p.List)
	if err != nil {
		return "", err
	}
	if p.Item != "" {
		if strings.Contains(strings.ToLower(contents), strings.ToLower(p.Item)) {
			return fmt.Sprintf("Yes, %s is on the %s list.", p.Item, p.List), nil
		}
		return fmt.Sprintf("No, %s is not on the %s list.", p.Item, p.List), nil
	}
	return contents, nil
}

func (h *HAClient) executeCheckOffItem(_ context.Context, args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return CheckOffItem(p.List, p.Item)
}

func (h *HAClient) executeUncheckItem(_ context.Context, args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return UncheckItem(p.List, p.Item)
}

func (h *HAClient) executeAddToList(_ context.Context, args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return AddToList(p.List, p.Item)
}

func (h *HAClient) callService(ctx context.Context, domain, service string, body map[string]any) error {
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

func sanitizeName(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}
