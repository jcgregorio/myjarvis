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
	"github.com/jcgregorio/myjarvis/internal/obsidian/lists"
	"github.com/jcgregorio/myjarvis/internal/obsidian/property"
)

// Searcher is the minimal rag surface the dispatcher needs.
// Declared as an interface so agent_test can inject a fake without
// importing internal/rag.
type Searcher interface {
	AnswerFromNotes(ctx context.Context, args string) (string, error)
	AnswerFromWikipedia(ctx context.Context, args string) (string, error)
}

// Dispatcher routes a tool call to the appropriate backend.
type Dispatcher struct {
	ha  *ha.Client
	rag Searcher // optional; if nil, search_* tools return an error
}

// New builds a Dispatcher. ragSearcher may be nil to disable search_*.
func New(haClient *ha.Client, ragSearcher Searcher) *Dispatcher {
	return &Dispatcher{ha: haClient, rag: ragSearcher}
}

// Execute dispatches the tool call. Returns the spoken reply (for
// tools that produce one) and any error.
func (d *Dispatcher) Execute(ctx context.Context, tc ha.ToolCall) (string, error) {
	switch tc.Name {
	case "set_state":
		return "", d.setState(ctx, tc.Args)
	case "set_temperature":
		return "", d.setTemperature(ctx, tc.Args)
	case "set_hvac_mode":
		return "", d.setHvacMode(ctx, tc.Args)
	case "trigger_automation":
		return "", d.triggerAutomation(ctx, tc.Args)
	case "set_timer":
		return "", d.setTimer(ctx, tc.Args)
	case "check_list":
		return d.checkList(tc.Args)
	case "check_off_item":
		return "", d.checkOffItem(tc.Args)
	case "uncheck_item":
		return "", d.uncheckItem(tc.Args)
	case "clean_lists":
		return "", lists.Clean()
	case "add_to_list":
		return "", d.addToList(tc.Args)
	case "log_property_event":
		return d.logPropertyEvent(tc.Args)
	case "search_notes":
		if d.rag == nil {
			return "", fmt.Errorf("rag not configured")
		}
		return d.rag.AnswerFromNotes(ctx, tc.Args)
	case "search_wikipedia":
		if d.rag == nil {
			return "", fmt.Errorf("rag not configured")
		}
		return d.rag.AnswerFromWikipedia(ctx, tc.Args)
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

func (d *Dispatcher) setTimer(ctx context.Context, args string) error {
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
	return d.ha.CallService(ctx, "timer", "start", map[string]any{
		"entity_id": "timer." + sanitizeName(p.Name),
		"duration":  fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds),
	})
}

func (d *Dispatcher) checkList(args string) (string, error) {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	contents, err := lists.Read(p.List)
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

func (d *Dispatcher) checkOffItem(args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return lists.CheckOff(p.List, p.Item)
}

func (d *Dispatcher) uncheckItem(args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return lists.Uncheck(p.List, p.Item)
}

func (d *Dispatcher) addToList(args string) error {
	var p struct {
		List string `json:"list"`
		Item string `json:"item"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	return lists.Add(p.List, p.Item)
}

func (d *Dispatcher) logPropertyEvent(args string) (string, error) {
	var p struct {
		Property    string `json:"property"`
		Description string `json:"description"`
		Hours       int    `json:"hours"`
		When        string `json:"when"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	return property.Log(p.Property, p.Hours, p.Description, p.When)
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

func (d *Dispatcher) setHvacMode(ctx context.Context, args string) error {
	var p struct {
		Entity string `json:"entity"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	entityID, ok := d.ha.LookupEntity(p.Entity)
	if !ok {
		return fmt.Errorf("unknown entity %q", p.Entity)
	}
	return d.ha.CallService(ctx, "climate", "set_hvac_mode", map[string]any{
		"entity_id": entityID,
		"hvac_mode": p.Mode,
	})
}

func sanitizeName(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}
