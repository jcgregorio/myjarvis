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

	tools = append(tools,
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name: "search_notes",
				Description: openai.String("Search the user's personal notes (Obsidian vault) to answer a question or produce a summary. " +
					"Use this for anything specific to the user: properties they own, their computers, cars, projects, schedule, todos, " +
					"or topics they've written about. NOT for general world knowledge — use search_wikipedia for that."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search keywords to find relevant notes (e.g. 'austin computer' or 'telluride oil')",
						},
						"question": map[string]any{
							"type":        "string",
							"description": "The user's original question or request (e.g. 'when did I buy the Hayes Run property?' or 'summarize my Goldmine Prime notes')",
						},
					},
					"required": []string{"query", "question"},
				},
			},
		},
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name: "search_wikipedia",
				Description: openai.String("Look up factual information from Wikipedia. " +
					"Use this for general knowledge questions about people, places, history, science, definitions — " +
					"anything that isn't specific to the user. NOT for personal notes — use search_notes for that."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search keywords to find relevant articles (e.g. 'transistor invention' or 'great barrier reef')",
						},
						"question": map[string]any{
							"type":        "string",
							"description": "The user's original question (e.g. 'who invented the transistor?')",
						},
					},
					"required": []string{"query", "question"},
				},
			},
		},
	)

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
				Name:        "check_list",
				Description: openai.String("Read the items on a list, or check if a specific item is on a list. Only returns unchecked (active) items."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"list": map[string]any{
							"type":        "string",
							"description": "The list to check",
							"enum":        listEnum,
						},
						"item": map[string]any{
							"type":        "string",
							"description": "Optional: a specific item to check for. If omitted, returns all items.",
						},
					},
					"required": []string{"list"},
				},
			},
		},
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "check_off_item",
				Description: openai.String("Mark an item as done on a list. Use this when the user says they got something, completed something, or wants to check off an item."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"list": map[string]any{
							"type":        "string",
							"description": "The list the item is on",
							"enum":        listEnum,
						},
						"item": map[string]any{
							"type":        "string",
							"description": "The item to check off",
						},
					},
					"required": []string{"list", "item"},
				},
			},
		},
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "uncheck_item",
				Description: openai.String("Uncheck a completed item on a list, marking it as active again."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"list": map[string]any{
							"type":        "string",
							"description": "The list the item is on",
							"enum":        listEnum,
						},
						"item": map[string]any{
							"type":        "string",
							"description": "The item to uncheck",
						},
					},
					"required": []string{"list", "item"},
				},
			},
		},
		openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "clean_lists",
				Description: openai.String("Remove all checked-off (completed) items from all lists. Use this when the user says to clean up the lists."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{},
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
