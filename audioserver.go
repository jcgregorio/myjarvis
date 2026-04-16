package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AudioServer serves generated TTS audio files over HTTP so ESP32 devices can fetch them.
type AudioServer struct {
	dir      string
	port     int
	hostAddr string // externally reachable address (e.g. 192.168.1.x:8080)
	mu       sync.Mutex
}

func NewAudioServer(port int) (*AudioServer, error) {
	dir, err := os.MkdirTemp("", "jarvis-audio-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	hostAddr, err := localIP()
	if err != nil {
		return nil, fmt.Errorf("determine local IP: %w", err)
	}

	s := &AudioServer{
		dir:      dir,
		port:     port,
		hostAddr: fmt.Sprintf("%s:%d", hostAddr, port),
	}

	mux := http.NewServeMux()
	mux.Handle("/audio/", http.StripPrefix("/audio/", http.FileServer(http.Dir(dir))))

	go func() {
		addr := fmt.Sprintf(":%d", port)
		log.Printf("[audioserver] serving on %s (files in %s)", addr, dir)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[audioserver] error: %v", err)
		}
	}()

	// Clean up old files periodically
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanup(5 * time.Minute)
		}
	}()

	return s, nil
}

// Store saves audio bytes and returns a URL the ESP32 can fetch.
func (s *AudioServer) Store(wav []byte) (string, error) {
	name := randomName() + ".wav"
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, wav, 0644); err != nil {
		return "", fmt.Errorf("write audio file: %w", err)
	}
	return fmt.Sprintf("http://%s/audio/%s", s.hostAddr, name), nil
}

func (s *AudioServer) cleanup(maxAge time.Duration) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

func randomName() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func localIP() (string, error) {
	conn, err := net.Dial("udp", "192.168.1.1:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), nil
}
