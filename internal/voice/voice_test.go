package voice

import "testing"

func TestIsStopCommand(t *testing.T) {
	stop := []string{
		"stop", "Stop", "STOP", "stop.", "  stop  ",
		"cancel", "cancel.",
		"shut up", "shut up.",
		"be quiet", "quiet", "quiet.",
		"never mind", "nevermind",
	}
	cont := []string{
		"", "stop the kitchen light", "do not stop",
		"please cancel my reservation", "be there in five",
		"hello", "what's the weather",
	}
	for _, s := range stop {
		if !IsStopCommand(s) {
			t.Errorf("IsStopCommand(%q) = false, want true", s)
		}
	}
	for _, s := range cont {
		if IsStopCommand(s) {
			t.Errorf("IsStopCommand(%q) = true, want false", s)
		}
	}
}

func TestContainsWakeWord(t *testing.T) {
	yes := []string{
		"hey jarvis turn on the kitchen light",
		"Hey, Jarvis, what's the weather",
		"JARVIS stop",
		"jarvis",
		"hello jarvis",
	}
	no := []string{
		"",
		// false-positive scenarios: mWW triggered, prebuffer included
		// real ambient speech, but no wake word in the transcript.
		"turn on the kitchen light",
		"the weather is nice",
		"hey siri",
		"i think so",
	}
	for _, s := range yes {
		if !ContainsWakeWord(s) {
			t.Errorf("ContainsWakeWord(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if ContainsWakeWord(s) {
			t.Errorf("ContainsWakeWord(%q) = true, want false", s)
		}
	}
}

func TestRunner_Tools(t *testing.T) {
	// Construct a Runner without any deps just to exercise Tools()'s
	// snapshot semantics — proves the package surface is reachable
	// for tests, which is the whole point of the extraction.
	r := New(Deps{})
	if got := r.Tools(); got != nil {
		t.Errorf("Tools() with nil InitialTools = %v, want nil", got)
	}
}
