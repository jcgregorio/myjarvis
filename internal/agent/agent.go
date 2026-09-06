// Package agent owns LLM tool-call dispatch. The Dispatcher takes a
// ha.ToolCall (the LLM's chosen tool + JSON args) and routes it to the
// right backend: ha.Client for Home Assistant actions, internal/rag for
// search_*, internal/obsidian/{lists,property} for vault writes.
//
// Pulling this out of the ha package broke the previously upside-down
// dependency where internal/ha was importing internal/rag, lists, and
// property purely because ExecuteToolCall lived in the wrong place.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jcgregorio/myjarvis/internal/ha"
)

// Dispatcher routes a tool call to the appropriate backend.
type Dispatcher struct {
	ha *ha.Client
}

// New builds a Dispatcher. ragSearcher may be nil to disable search_*.
func New(haClient *ha.Client) *Dispatcher {
	return &Dispatcher{ha: haClient}
}

// Execute dispatches the tool call. Returns the spoken reply (for
// tools that produce one) and any error.
func (d *Dispatcher) Execute(ctx context.Context, tc ha.ToolCall) (string, error) {
	switch tc.Name {
	case "set_state":
		return "", d.setState(ctx, tc.Args)
	case "set_temperature":
		return "", d.setTemperature(ctx, tc.Args)
	case "trigger_automation":
		return "", d.triggerAutomation(ctx, tc.Args)
	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}
}

func (d *Dispatcher) setState(ctx context.Context, args string) error {
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
	entityID, ok := d.ha.LookupEntity(p.Entity)
	if !ok {
		return fmt.Errorf("unknown entity %q", p.Entity)
	}
	domain, _, _ := strings.Cut(entityID, ".")
	return d.ha.CallService(ctx, domain, "turn_"+p.State, map[string]any{"entity_id": entityID})
}

func (d *Dispatcher) triggerAutomation(ctx context.Context, args string) error {
	var p struct {
		Entity string `json:"entity"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	entityID, ok := d.ha.LookupEntity(p.Entity)
	if !ok {
		return fmt.Errorf("unknown automation %q", p.Entity)
	}
	domain, _, _ := strings.Cut(entityID, ".")
	service := "trigger"
	if domain == "script" {
		service = "turn_on"
	}
	return d.ha.CallService(ctx, domain, service, map[string]any{"entity_id": entityID})
}

func (d *Dispatcher) setTemperature(ctx context.Context, args string) error {
	var p struct {
		Entity      string  `json:"entity"`
		Temperature float64 `json:"temperature"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	entityID, ok := d.ha.LookupEntity(p.Entity)
	if !ok {
		return fmt.Errorf("unknown entity %q", p.Entity)
	}
	return d.ha.CallService(ctx, "climate", "set_temperature", map[string]any{
		"entity_id":   entityID,
		"temperature": p.Temperature,
	})
}

func sanitizeName(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}
