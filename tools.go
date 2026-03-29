package main

import (
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// BuildTools constructs the OpenAI tool schema from the list of HA entities.
// The entity_id enum is populated dynamically so the LLM can only reference
// entities that actually exist in the HA instance.
func BuildTools(entities []HAEntity) []openai.ChatCompletionToolParam {
	entityIDs := make([]any, len(entities))
	for i, e := range entities {
		entityIDs[i] = e.EntityID
	}

	return []openai.ChatCompletionToolParam{
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "set_state",
				Description: openai.String("Turn a Home Assistant entity on or off"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"entity_id": map[string]any{
							"type":        "string",
							"description": "The entity to control",
							"enum":        entityIDs,
						},
						"state": map[string]any{
							"type": "string",
							"enum": []string{"on", "off"},
						},
					},
					"required": []string{"entity_id", "state"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "set_timer",
				Description: openai.String("Start a countdown timer"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "A short name for the timer (e.g. pasta, laundry)",
						},
						"duration_seconds": map[string]any{
							"type":        "integer",
							"description": "Duration in seconds",
						},
					},
					"required": []string{"name", "duration_seconds"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "add_shopping_item",
				Description: openai.String("Add an item to the Home Assistant shopping list"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"item": map[string]any{
							"type": "string",
						},
					},
					"required": []string{"item"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "add_todo",
				Description: openai.String("Add a task to a Home Assistant to-do list"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"list": map[string]any{
							"type":        "string",
							"description": "Name of the to-do list",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "The task to add",
						},
					},
					"required": []string{"list", "task"},
				},
			},
		},
	}
}
