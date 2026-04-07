package main

import (
	"context"
	"regexp"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// qwen2.5 sometimes returns tool calls as text in the form:
// )(((tool_name {"arg": "val"})))
// This regex extracts the tool name and JSON args from that format.
var textToolCallRe = regexp.MustCompile(`\(\(\((\w+)\s+(\{.*?\})\)\)\)`)

const systemPrompt = `You are a home assistant voice controller. When the user gives a command, call the appropriate tool to execute it. Only make tool calls — do not respond with prose unless no tool applies. If the command is ambiguous, make a reasonable assumption.`

type LLMClient struct {
	client openai.Client
	model  string
}

func NewLLMClient(baseURL, model string) *LLMClient {
	return &LLMClient{
		client: openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey("ollama"), // Ollama ignores the key but the SDK requires a non-empty value
		),
		model: model,
	}
}

// Chat sends the user input to the LLM with the tool list and returns either
// a slice of tool calls to execute or a plain text reply.
func (l *LLMClient) Chat(ctx context.Context, userInput string, tools []openai.ChatCompletionToolParam) ([]ToolCall, string, error) {
	completion, err := l.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: l.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userInput),
		},
		Tools: tools,
	})
	if err != nil {
		return nil, "", err
	}

	msg := completion.Choices[0].Message

	if len(msg.ToolCalls) == 0 {
		// Fallback: parse text-format tool calls (qwen2.5 quirk)
		matches := textToolCallRe.FindAllStringSubmatch(msg.Content, -1)
		if len(matches) == 0 {
			return nil, msg.Content, nil
		}
		var calls []ToolCall
		for _, m := range matches {
			calls = append(calls, ToolCall{Name: m[1], Args: m[2]})
		}
		return calls, "", nil
	}

	calls := make([]ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		calls = append(calls, ToolCall{
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	return calls, "", nil
}
