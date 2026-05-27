package ha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEntities is a minimal HA /api/states response covering several domains.
var fakeEntities = []Entity{
	{EntityID: "light.kitchen", State: "off"},
	{EntityID: "light.living_room", State: "on"},
	{EntityID: "switch.fan", State: "off"},
	{EntityID: "sensor.temperature", State: "21.5"},   // non-controllable
	{EntityID: "binary_sensor.door", State: "off"},    // non-controllable
	{EntityID: "person.john", State: "home"},          // non-controllable
	{EntityID: "input_boolean.guest_mode", State: "on"},
	{EntityID: "media_player.tv", State: "idle"},
}

func newFakeHAServer(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/states":
			json.NewEncoder(w).Encode(fakeEntities)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewClient(srv.URL, "fake-token")
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

func TestLookupEntity(t *testing.T) {
	c := NewClient("http://example", "tok")
	c.SetEntities(map[string]string{
		"kitchen light": "light.kitchen",
		"living room":   "light.living_room",
	})
	if id, ok := c.LookupEntity("Kitchen Light"); !ok || id != "light.kitchen" {
		t.Errorf("LookupEntity(Kitchen Light) = (%q, %v)", id, ok)
	}
	if _, ok := c.LookupEntity("nope"); ok {
		t.Error("LookupEntity(nope) should be false")
	}
}
