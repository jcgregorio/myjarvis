package main

import (
	"net"
	"os"
	"testing"
)

func TestSynthesize(t *testing.T) {
	addr := "localhost:10200"
	// Skip if Piper isn't running
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Skipf("piper not reachable at %s: %v", addr, err)
	}
	conn.Close()

	tts := NewTTSClient(addr)
	wav, err := tts.Synthesize("Austin's computer has an AMD Ryzen 7 5700X CPU.")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if len(wav) < 44 {
		t.Fatalf("WAV too short: %d bytes", len(wav))
	}

	// Check WAV header
	if string(wav[:4]) != "RIFF" {
		t.Errorf("expected RIFF header, got %q", string(wav[:4]))
	}
	if string(wav[8:12]) != "WAVE" {
		t.Errorf("expected WAVE format, got %q", string(wav[8:12]))
	}

	t.Logf("Generated %d bytes of WAV audio", len(wav))

	// Optionally write to file for manual inspection
	if os.Getenv("TTS_SAVE") != "" {
		os.WriteFile("/tmp/tts-test.wav", wav, 0644)
		t.Logf("Saved to /tmp/tts-test.wav")
	}
}
