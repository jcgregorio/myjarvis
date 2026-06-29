package llm

import (
	"encoding/json"
	"testing"
	"github.com/jcgregorio/myjarvis/internal/ha"
)

func TestBuildTools_names(t *testing.T) {
	tools := BuildTools(nil, nil, nil)
	want := []string{"set_state", "search_notes", "search_wikipedia", "set_timer", "check_list", "check_off_item", "uncheck_item", "clean_lists", "add_to_list", "log_property_event"}
	if len(tools) != len(want) {
		got := make([]string, len(tools))
		for i, tool := range tools {
			got[i] = tool.Function.Name
		}
		t.Fatalf("got %d tools %v, want %d %v", len(tools), got, len(want), want)
	}
	for i, w := range want {
		if got := tools[i].Function.Name; got != w {
			t.Errorf("tools[%d].Name = %q, want %q", i, got, w)
		}
	}
}

func TestBuildTools_climateTools(t *testing.T) {
	entities := []ha.Entity{
		{EntityID: "climate.downstairs", Attributes: map[string]any{"friendly_name": "Downstairs Thermostat"}},
		{EntityID: "light.kitchen"},
	}
	tools := BuildTools(entities, nil, nil)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}

	var foundTemp, foundMode bool
	for _, n := range names {
		if n == "set_temperature" {
			foundTemp = true
		}
		if n == "set_hvac_mode" {
			foundMode = true
		}
	}
	if !foundTemp {
		t.Errorf("expected set_temperature tool, got %v", names)
	}
	if !foundMode {
		t.Errorf("expected set_hvac_mode tool, got %v", names)
	}

	// Verify climate entity appears in the enum for set_temperature.
	for _, tool := range tools {
		if tool.Function.Name != "set_temperature" {
			continue
		}
		raw, _ := json.Marshal(tool.Function.Parameters)
		var schema map[string]any
		json.Unmarshal(raw, &schema)
		props := schema["properties"].(map[string]any)
		enum := props["entity"].(map[string]any)["enum"].([]any)
		if len(enum) != 1 || enum[0] != "downstairs thermostat" {
			t.Errorf("climate enum = %v, want [downstairs thermostat]", enum)
		}
	}
}

func TestBuildTools_noClimateTools(t *testing.T) {
	entities := []ha.Entity{
		{EntityID: "light.kitchen"},
	}
	tools := BuildTools(entities, nil, nil)
	for _, tool := range tools {
		if tool.Function.Name == "set_temperature" || tool.Function.Name == "set_hvac_mode" {
			t.Errorf("unexpected climate tool %q when no climate entities present", tool.Function.Name)
		}
	}
}

func TestBuildTools_entityIDEnum(t *testing.T) {
	entities := []ha.Entity{
		{EntityID: "light.kitchen"},
		{EntityID: "switch.fan"},
	}
	tools := BuildTools(entities, nil, nil)

	// set_state is the first tool
	params := tools[0].Function.Parameters
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)
	entityProp := props["entity"].(map[string]any)
	enum := entityProp["enum"].([]any)

	if len(enum) != 2 {
		t.Fatalf("enum has %d entries, want 2", len(enum))
	}
	if enum[0] != "light.kitchen" || enum[1] != "switch.fan" {
		t.Errorf("unexpected enum values: %v", enum)
	}
}

func TestBuildTools_emptyEntities(t *testing.T) {
	// Should not panic with zero entities
	tools := BuildTools([]ha.Entity{}, nil, nil)
	if len(tools) == 0 {
		t.Fatal("expected tools even with no entities")
	}
	params := tools[0].Function.Parameters
	raw, _ := json.Marshal(params)
	var schema map[string]any
	json.Unmarshal(raw, &schema)
	props := schema["properties"].(map[string]any)
	entityProp := props["entity"].(map[string]any)
	enum := entityProp["enum"].([]any)
	if len(enum) != 0 {
		t.Errorf("expected empty enum, got %v", enum)
	}
}
