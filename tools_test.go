package main

import (
	"encoding/json"
	"testing"
)

func TestBuildTools_names(t *testing.T) {
	tools := BuildTools(nil, nil)
	want := []string{"set_state", "search_notes", "summarize_notes", "set_timer", "check_list", "check_off_item", "uncheck_item", "clean_lists", "add_to_list"}
	if len(tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(tools), len(want))
	}
	for i, w := range want {
		if got := tools[i].Function.Name; got != w {
			t.Errorf("tools[%d].Name = %q, want %q", i, got, w)
		}
	}
}

func TestBuildTools_entityIDEnum(t *testing.T) {
	entities := []HAEntity{
		{EntityID: "light.kitchen"},
		{EntityID: "switch.fan"},
	}
	tools := BuildTools(entities, nil)

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
	tools := BuildTools([]HAEntity{}, nil)
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
