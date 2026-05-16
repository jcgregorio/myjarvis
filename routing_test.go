package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
	"time"
)

// routing_test.go is an integration test that exercises the real LLM
// (Ollama) to verify that prompts are routed to the correct tool, and
// reports per-category routing latency. It is skipped unless OLLAMA_URL
// is set, so the normal `go test ./...` / `make test` is unaffected.
//
// Run it with:  make test-routing
//
// It measures only the routing call (a single LLM round-trip). The
// user-perceived latency for search_notes / search_wikipedia adds a
// second LLM call (the RAG answer synthesis) which this test does not
// execute — it deliberately avoids side effects (no HA actuation, no
// list mutation, no RAG sidecar dependency).

// fixtureEntities is a fixed synthetic HA entity set so routing results
// are reproducible regardless of the live Home Assistant state.
func fixtureEntities() []HAEntity {
	mk := func(id, name string) HAEntity {
		return HAEntity{EntityID: id, Attributes: map[string]any{"friendly_name": name}}
	}
	return []HAEntity{
		mk("light.kitchen", "Kitchen Light"),
		mk("light.living_room", "Living Room Light"),
		mk("light.bedroom", "Bedroom Light"),
		mk("switch.fan", "Fan"),
		mk("switch.coffee_maker", "Coffee Maker"),
		mk("fan.office", "Office Fan"),
		mk("automation.goodnight", "Goodnight"),
		mk("script.movie_time", "Movie Time"),
	}
}

var fixtureLists = []string{"ShoppingList", "TodoList"}

type routeCase struct {
	category string
	prompt   string
	wantTool string
}

// routeCases covers the three target capabilities plus the supporting
// list/timer tools. wantTool is the tool the router should select.
var routeCases = []routeCase{
	// --- Home automation: on/off device control ---
	{"home/set_state", "Turn on the kitchen light", "set_state"},
	{"home/set_state", "Turn off the living room light", "set_state"},
	{"home/set_state", "Switch off the fan", "set_state"},
	{"home/set_state", "Bedroom light on please", "set_state"},
	{"home/set_state", "Can you turn the coffee maker on", "set_state"},
	{"home/set_state", "Shut off the office fan", "set_state"},

	// --- Home automation: automations / scripts ---
	{"home/trigger_automation", "Run the goodnight routine", "trigger_automation"},
	{"home/trigger_automation", "Activate movie time", "trigger_automation"},

	// --- Home automation: timers ---
	{"home/set_timer", "Set a timer for 10 minutes", "set_timer"},
	{"home/set_timer", "Start a 5 minute pasta timer", "set_timer"},

	// --- Home automation: lists ---
	{"home/add_to_list", "Add milk to the shopping list", "add_to_list"},
	{"home/add_to_list", "Put batteries on the todo list", "add_to_list"},
	{"home/check_list", "What's on my shopping list", "check_list"},
	{"home/check_list", "Is bread on the shopping list", "check_list"},
	{"home/check_off_item", "Check off bread from the shopping list", "check_off_item"},
	{"home/clean_lists", "Clean up the lists", "clean_lists"},

	// --- Obsidian: personal / "about me" knowledge ---
	{"obsidian/search_notes", "What did I write about Goldmine Prime", "search_notes"},
	{"obsidian/search_notes", "When did I buy the Hayes Run property", "search_notes"},
	{"obsidian/search_notes", "Summarize my notes on the Telluride trip", "search_notes"},
	{"obsidian/search_notes", "What are the specs of my Austin computer", "search_notes"},
	{"obsidian/search_notes", "Remind me what I wrote about the RAG setup", "search_notes"},
	{"obsidian/search_notes", "What did I write in my notes about the basement renovation", "search_notes"},
	{"obsidian/search_notes", "What did I note about my car's last oil change", "search_notes"},

	// --- Wikipedia: general world knowledge ---
	{"wiki/search_wikipedia", "Who invented the transistor", "search_wikipedia"},
	{"wiki/search_wikipedia", "How far is the moon from the earth in light seconds", "search_wikipedia"},
	{"wiki/search_wikipedia", "What is the capital of Mongolia", "search_wikipedia"},
	{"wiki/search_wikipedia", "When did World War 2 end", "search_wikipedia"},
	{"wiki/search_wikipedia", "What is the speed of light", "search_wikipedia"},
	{"wiki/search_wikipedia", "How tall is Mount Everest", "search_wikipedia"},
	{"wiki/search_wikipedia", "Who wrote Pride and Prejudice", "search_wikipedia"},
	{"wiki/search_wikipedia", "Explain how photosynthesis works", "search_wikipedia"},
	{"wiki/search_wikipedia", "What year did the Berlin Wall fall", "search_wikipedia"},
	{"wiki/search_wikipedia", "What is the boiling point of water in Fahrenheit", "search_wikipedia"},
}

type routeResult struct {
	category string
	prompt   string
	want     string
	got      string
	latency  time.Duration
	ok       bool
}

func TestRouting(t *testing.T) {
	baseURL := os.Getenv("OLLAMA_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_URL not set — skipping live routing test (run via `make test-routing`)")
	}
	model := os.Getenv("MODEL")
	if model == "" {
		model = "qwen3:14b-64k"
	}

	llm := NewLLMClient(baseURL, model)
	tools := BuildTools(fixtureEntities(), fixtureLists)

	// MYJARVIS_NOTHINK=1 appends qwen3's /no_think soft switch so the
	// router skips chain-of-thought — used for the latency comparison.
	noThink := os.Getenv("MYJARVIS_NOTHINK") == "1"
	decorate := func(p string) string {
		if noThink {
			return p + " /no_think"
		}
		return p
	}

	// Warm up the target model. Switching MODEL between comparison runs
	// forces a cold load; this throwaway call absorbs it so all measured
	// calls below are warm and comparable.
	{
		wctx, wcancel := context.WithTimeout(context.Background(), 180*time.Second)
		_, _, _ = llm.Chat(wctx, decorate("turn on the kitchen light"), tools)
		wcancel()
	}

	t.Logf("routing test: model=%s endpoint=%s cases=%d no_think=%v", model, baseURL, len(routeCases), noThink)

	results := make([]routeResult, 0, len(routeCases))
	for _, c := range routeCases {
		c := c
		t.Run(c.category+"/"+c.prompt, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			start := time.Now()
			toolCalls, reply, err := llm.Chat(ctx, decorate(c.prompt), tools)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("Chat error after %s: %v", elapsed.Round(time.Millisecond), err)
			}

			got := "(none)"
			if len(toolCalls) > 0 {
				got = toolCalls[0].Name
			}
			ok := got == c.wantTool
			results = append(results, routeResult{c.category, c.prompt, c.wantTool, got, elapsed, ok})

			if !ok {
				if got == "(none)" {
					t.Errorf("routed to NO tool (plain reply: %q) in %s; want %s",
						truncate(reply, 80), elapsed.Round(time.Millisecond), c.wantTool)
				} else {
					t.Errorf("routed to %s in %s; want %s",
						got, elapsed.Round(time.Millisecond), c.wantTool)
				}
			} else {
				t.Logf("ok %s in %s", got, elapsed.Round(time.Millisecond))
			}
		})
	}

	reportSummary(t, results, model, noThink)
}

func reportSummary(t *testing.T, results []routeResult, model string, noThink bool) {
	if len(results) == 0 {
		return
	}

	// Aggregate by category and overall.
	type bucket struct {
		total, pass int
		lats        []time.Duration
	}
	byCat := map[string]*bucket{}
	all := &bucket{}
	for _, r := range results {
		b := byCat[r.category]
		if b == nil {
			b = &bucket{}
			byCat[r.category] = b
		}
		b.total++
		all.total++
		b.lats = append(b.lats, r.latency)
		all.lats = append(all.lats, r.latency)
		if r.ok {
			b.pass++
			all.pass++
		}
	}

	line := func(name string, b *bucket) string {
		sort.Slice(b.lats, func(i, j int) bool { return b.lats[i] < b.lats[j] })
		return fmt.Sprintf("%-28s %2d/%-2d (%3.0f%%)  min=%-6s p50=%-6s p95=%-6s max=%-6s mean=%-6s",
			name, b.pass, b.total, 100*float64(b.pass)/float64(b.total),
			rnd(b.lats[0]), rnd(pctile(b.lats, 50)), rnd(pctile(b.lats, 95)),
			rnd(b.lats[len(b.lats)-1]), rnd(meanDur(b.lats)))
	}

	cats := make([]string, 0, len(byCat))
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)

	t.Log("")
	t.Log("==================== ROUTING SUMMARY ====================")
	t.Log("category                     pass        latency (routing call only)")
	for _, c := range cats {
		t.Log(line(c, byCat[c]))
	}
	t.Log("---------------------------------------------------------")
	t.Log(line("OVERALL", all)) // sorts all.lats ascending as a side effect
	t.Log("=========================================================")

	// Machine-readable single line for the model-sweep script to parse.
	// Fields: model,no_think(0/1),pass,total,accuracy%,min,p50,p95,max,mean (ms)
	nt := 0
	if noThink {
		nt = 1
	}
	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	t.Logf("ROUTING_CSV:%s,%d,%d,%d,%.0f,%d,%d,%d,%d,%d",
		model, nt, all.pass, all.total, 100*float64(all.pass)/float64(all.total),
		ms(all.lats[0]), ms(pctile(all.lats, 50)), ms(pctile(all.lats, 95)),
		ms(all.lats[len(all.lats)-1]), ms(meanDur(all.lats)))

	// List misroutes explicitly so failures are easy to scan.
	var misses []routeResult
	for _, r := range results {
		if !r.ok {
			misses = append(misses, r)
		}
	}
	if len(misses) > 0 {
		t.Logf("%d misroute(s):", len(misses))
		for _, m := range misses {
			t.Logf("  [%s] %q -> got %s, want %s (%s)",
				m.category, m.prompt, m.got, m.want, rnd(m.latency))
		}
	}
}

func pctile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func meanDur(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	return sum / time.Duration(len(ds))
}

func rnd(d time.Duration) time.Duration { return d.Round(10 * time.Millisecond) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
