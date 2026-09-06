package llm

import (
	"strings"

	"github.com/jcgregorio/myjarvis/internal/ha"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// BuildTools constructs the OpenAI tool schema from the list of HA entities.
// The entity_id enum is populated dynamically so the LLM can only reference
// entities that actually exist in the HA instance.
func BuildTools(entities []ha.Entity) []openai.ChatCompletionToolParam {
	entityNames := make([]any, 0)
	var automationNames []any
	var climateNames []any
	for _, e := range entities {
		name := strings.ToLower(e.FriendlyName())
		domain, _, _ := strings.Cut(e.EntityID, ".")
		if domain == "automation" {
			automationNames = append(automationNames, name)
		} else if domain == "climate" {
			climateNames = append(climateNames, name)
			entityNames = append(entityNames, name)
		} else {
			entityNames = append(entityNames, name)
		}
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
				Description: openai.String("Trigger a Home Assistant automation scene or script to run"),
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

	if len(climateNames) > 0 {
		tools = append(tools,
			openai.ChatCompletionToolParam{
				Function: shared.FunctionDefinitionParam{
					Name:        "set_temperature",
					Description: openai.String("Set the target temperature on a thermostat"),
					Parameters: shared.FunctionParameters{
						"type": "object",
						"properties": map[string]any{
							"entity": map[string]any{
								"type":        "string",
								"description": "The name of the thermostat",
								"enum":        climateNames,
							},
							"temperature": map[string]any{
								"type":        "number",
								"description": "Target temperature in Fahrenheit",
							},
						},
						"required": []string{"entity", "temperature"},
					},
				},
			},
		)
	}

	return tools
}
