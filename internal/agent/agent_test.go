package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jcgregorio/myjarvis/internal/ha"
)

// seededClient returns a *ha.Client backed by the fake HA server and
// pre-seeded with a friendly-name → entity_id map, so dispatch tests
// don't have to round-trip /api/states.
func seededClient(t *testing.T) (*httptest.Server, *ha.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Default OK; per-test handlers override.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	c := ha.NewClient(srv.URL, "fake-token")
	c.SetEntities(map[string]string{
		"light.kitchen":  "light.kitchen",
		"light.living_room": "light.living_room",
		"switch.fan":     "switch.fan",
	})
	return srv, c
}

func TestExecute_setStateOff(t *testing.T) {
	srv, hc := seededClient(t)
	var gotPath string
	var gotBody map[string]any
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	d := New(hc, nil)
	if _, err := d.Execute(context.Background(), ha.ToolCall{
		Name: "set_state",
		Args: `{"entity":"light.kitchen","state":"off"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/services/light/turn_off" {
		t.Errorf("path = %q, want /api/services/light/turn_off", gotPath)
	}
	if gotBody["entity_id"] != "light.kitchen" {
		t.Errorf("entity_id = %v, want light.kitchen", gotBody["entity_id"])
	}
}

func TestExecute_setStateOn(t *testing.T) {
	srv, hc := seededClient(t)
	var gotPath string
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	d := New(hc, nil)
	if _, err := d.Execute(context.Background(), ha.ToolCall{
		Name: "set_state",
		Args: `{"entity":"switch.fan","state":"on"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/services/switch/turn_on" {
		t.Errorf("path = %q, want /api/services/switch/turn_on", gotPath)
	}
}

func TestExecute_setTimer(t *testing.T) {
	srv, hc := seededClient(t)
	var gotBody map[string]any
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	d := New(hc, nil)
	if _, err := d.Execute(context.Background(), ha.ToolCall{
		Name: "set_timer",
		Args: `{"name":"pasta","duration_seconds":600}`,
	}); err != nil {
		t.Fatal(err)
	}
	if gotBody["duration"] != "00:10:00" {
		t.Errorf("duration = %v, want 00:10:00", gotBody["duration"])
	}
	if gotBody["entity_id"] != "timer.pasta" {
		t.Errorf("entity_id = %v, want timer.pasta", gotBody["entity_id"])
	}
}

func TestExecute_invalidState(t *testing.T) {
	_, hc := seededClient(t)
	d := New(hc, nil)
	_, err := d.Execute(context.Background(), ha.ToolCall{
		Name: "set_state",
		Args: `{"entity":"light.kitchen","state":"maybe"}`,
	})
	if err == nil {
		t.Error("expected error for invalid state, got nil")
	}
}

func TestExecute_unknownTool(t *testing.T) {
	_, hc := seededClient(t)
	d := New(hc, nil)
	if _, err := d.Execute(context.Background(), ha.ToolCall{Name: "bogus"}); err == nil {
		t.Error("expected error for unknown tool, got nil")
	}
}

func TestExecute_searchNoRag(t *testing.T) {
	_, hc := seededClient(t)
	d := New(hc, nil) // no rag configured
	if _, err := d.Execute(context.Background(), ha.ToolCall{
		Name: "search_wikipedia",
		Args: `{"query":"x","question":"y"}`,
	}); err == nil {
		t.Error("expected error when rag is nil, got nil")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"pasta", "pasta"},
		{"Laundry Timer", "laundry_timer"},
		{"  eggs  ", "eggs"},
		{"My Shopping List", "my_shopping_list"},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in); got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
