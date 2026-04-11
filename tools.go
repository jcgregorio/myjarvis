package main

import (
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// BuildTools constructs the OpenAI tool schema from the list of HA entities.
// The entity_id enum is populated dynamically so the LLM can only reference
// entities that actually exist in the HA instance.
func BuildTools(entities []HAEntity, listNames []string) []openai.ChatCompletionToolParam {
	entityNames := make([]any, 0)
	var automationNames []any
	for _, e := range entities {
		name := strings.ToLower(e.FriendlyName())
		domain, _, _ := strings.Cut(e.EntityID, ".")
		if domain == "automation" || domain == "script" {
			automationNames = append(automationNames, name)
		} else {
			entityNames = append(entityNames, name)
		}
	}
	listEnum := make([]any, len(listNames))
	for i, n := range listNames {
		listEnum[i] = n
	}

	tools := []openai.ChatCompletionToolParam{
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
	}

	if len(automationNames) > 0 {
		tools = append(tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "trigger_automation",
				Description: openai.String("Trigger a Home Assistant automation or script to run"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"entity": map[string]any{
							"type":        "string",
							"description": "The name of the automation to trigger",
							"enum":        automationNames,
						},
					},
					"required": []string{"entity"},
				},
			},
		})
	}

	return append(tools,
		openai.ChatCompletionToolParam{
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
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "add_to_list",
				Description: openai.String(`Add an item to a list. Use list "ShoppingList" for groceries (default if not specified).`),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"item": map[string]any{
							"type":        "string",
							"description": "The item or task to add",
						},
						"list": map[string]any{
							"type":        "string",
							"description": `The list to add to. Defaults to "ShoppingList" if not specified.`,
							"enum":        listEnum,
						},
					},
					"required": []string{"item"},
				},
			},
		},
	)
}
