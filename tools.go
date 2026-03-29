package main

import (
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// BuildTools constructs the OpenAI tool schema from the list of HA entities.
// The entity_id enum is populated dynamically so the LLM can only reference
// entities that actually exist in the HA instance.
func BuildTools(entities []HAEntity) []openai.ChatCompletionToolParam {
	entityNames := make([]any, len(entities))
	for i, e := range entities {
		entityNames[i] = e.FriendlyName()
	}

	return []openai.ChatCompletionToolParam{
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "set_state",
				Description: openai.String("Turn a Home Assistant entity on or off"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"entity": map[string]any{
							"type":        "string",
							"description": "The name of the entity to control",
							"enum":        entityNames,
						},
						"state": map[string]any{
							"type": "string",
							"enum": []string{"on", "off"},
						},
					},
					"required": []string{"entity", "state"},
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
				Name:        "add_to_list",
				Description: openai.String(`Add an item to a list. Use list "Shopping List" for groceries (default if not specified). Use the to-do list name for other lists.`),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"item": map[string]any{
							"type":        "string",
							"description": "The item or task to add",
						},
						"list": map[string]any{
							"type":        "string",
							"description": `The list to add to. Defaults to "Shopping List" if not specified.`,
						},
					},
					"required": []string{"item"},
				},
			},
		},
	}
}
