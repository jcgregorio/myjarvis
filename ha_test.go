package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEntities is a minimal HA /api/states response covering several domains.
var fakeEntities = []HAEntity{
	{EntityID: "light.kitchen", State: "off"},
	{EntityID: "light.living_room", State: "on"},
	{EntityID: "switch.fan", State: "off"},
	{EntityID: "sensor.temperature", State: "21.5"},   // non-controllable
	{EntityID: "binary_sensor.door", State: "off"},     // non-controllable
	{EntityID: "person.john", State: "home"},           // non-controllable
	{EntityID: "input_boolean.guest_mode", State: "on"},
	{EntityID: "media_player.tv", State: "idle"},
}

// newFakeHAClientWithNames returns an HAClient pre-loaded with a name→ID map.
func newFakeHAClientWithNames(srv *httptest.Server) *HAClient {
	ha := NewHAClient(srv.URL, "fake-token")
	for _, e := range fakeEntities {
		ha.nameToID[e.FriendlyName()] = e.EntityID
	}
	return ha
}

func newFakeHAServer(t *testing.T) (*httptest.Server, *HAClient) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/states":
			json.NewEncoder(w).Encode(fakeEntities)
		case "/api/services/light/turn_off",
			"/api/services/light/turn_on",
			"/api/services/switch/turn_on",
			"/api/services/switch/turn_off",
			"/api/services/timer/start",
			"/api/services/shopping_list/add_item",
			"/api/services/todo/add_item":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("[]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewHAClient(srv.URL, "fake-token")
}

func TestFetchControllableEntities_filters(t *testing.T) {
	_, ha := newFakeHAServer(t)
	entities, err := ha.FetchControllableEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// sensor, binary_sensor, person should be excluded
	for _, e := range entities {
		domain, _, _ := strings.Cut(e.EntityID, ".")
		if !controllableDomains[domain] {
			t.Errorf("got non-controllable entity: %s", e.EntityID)
		}
	}

	// lights, switch, input_boolean, media_player should be included
	ids := make(map[string]bool)
	for _, e := range entities {
		ids[e.EntityID] = true
	}
	for _, want := range []string{"light.kitchen", "light.living_room", "switch.fan", "input_boolean.guest_mode", "media_player.tv"} {
		if !ids[want] {
			t.Errorf("expected entity %s to be included", want)
		}
	}
	for _, notWant := range []string{"sensor.temperature", "binary_sensor.door", "person.john"} {
		if ids[notWant] {
			t.Errorf("expected entity %s to be excluded", notWant)
		}
	}
}

func TestExecuteToolCall_setStateOff(t *testing.T) {
	srv, _ := newFakeHAServer(t)
	ha := newFakeHAClientWithNames(srv)
	var gotPath string
	var gotBody map[string]any
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	_, err := ha.ExecuteToolCall(context.Background(), ToolCall{
		Name: "set_state",
		Args: `{"entity":"light.kitchen","state":"off"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/services/light/turn_off" {
		t.Errorf("path = %q, want /api/services/light/turn_off", gotPath)
	}
	if gotBody["entity_id"] != "light.kitchen" {
		t.Errorf("entity_id = %v, want light.kitchen", gotBody["entity_id"])
	}
}

func TestExecuteToolCall_setStateOn(t *testing.T) {
	srv, _ := newFakeHAServer(t)
	ha := newFakeHAClientWithNames(srv)
	var gotPath string
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	_, err := ha.ExecuteToolCall(context.Background(), ToolCall{
		Name: "set_state",
		Args: `{"entity":"switch.fan","state":"on"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/services/switch/turn_on" {
		t.Errorf("path = %q, want /api/services/switch/turn_on", gotPath)
	}
}

// Note: add_to_list tests are in lists_test.go since they now use file-based storage.

func TestExecuteToolCall_setTimer(t *testing.T) {
	srv, ha := newFakeHAServer(t)
	var gotBody map[string]any
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	_, err := ha.ExecuteToolCall(context.Background(), ToolCall{
		Name: "set_timer",
		Args: `{"name":"pasta","duration_seconds":600}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["duration"] != "00:10:00" {
		t.Errorf("duration = %v, want 00:10:00", gotBody["duration"])
	}
	if gotBody["entity_id"] != "timer.pasta" {
		t.Errorf("entity_id = %v, want timer.pasta", gotBody["entity_id"])
	}
}

func TestExecuteToolCall_invalidState(t *testing.T) {
	srv, _ := newFakeHAServer(t)
	ha := newFakeHAClientWithNames(srv)
	_, err := ha.ExecuteToolCall(context.Background(), ToolCall{
		Name: "set_state",
		Args: `{"entity":"light.kitchen","state":"maybe"}`,
	})
	if err == nil {
		t.Error("expected error for invalid state, got nil")
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
