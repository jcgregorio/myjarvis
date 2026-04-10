package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

// VoiceMQTTClient subscribes to voice device MQTT topics and routes audio
// through an AudioRouter.
type VoiceMQTTClient struct {
	cm     *autopaho.ConnectionManager
	router *AudioRouter
}

// NewVoiceMQTTClient connects to the MQTT broker and subscribes to
// jarvis/+/audio_start, jarvis/+/audio, and jarvis/+/audio_stop.
func NewVoiceMQTTClient(ctx context.Context, brokerURL string, router *AudioRouter) (*VoiceMQTTClient, error) {
	u, err := url.Parse(brokerURL)
	if err != nil {
		return nil, fmt.Errorf("parse broker URL: %w", err)
	}

	v := &VoiceMQTTClient{router: router}

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		CleanStartOnInitialConnection: true,
		KeepAlive:                     30,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			log.Println("[mqtt] connected to broker")
			if _, err := cm.Subscribe(ctx, &paho.Subscribe{
				Subscriptions: []paho.SubscribeOptions{
					{Topic: "jarvis/+/audio_start", QoS: 0},
					{Topic: "jarvis/+/audio", QoS: 0},
					{Topic: "jarvis/+/audio_stop", QoS: 0},
					{Topic: "jarvis/+/wake_detected", QoS: 0},
				},
			}); err != nil {
				log.Printf("[mqtt] subscribe error: %v", err)
			}
		},
		OnConnectError: func(err error) {
			log.Printf("[mqtt] connection error: %v", err)
		},
		ClientConfig: paho.ClientConfig{
			ClientID: "myjarvis",
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				v.handleMessage,
			},
		},
	}

	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mqtt connect: %w", err)
	}
	v.cm = cm
	return v, nil
}

// handleMessage routes incoming MQTT messages to the AudioRouter.
func (v *VoiceMQTTClient) handleMessage(pr paho.PublishReceived) (bool, error) {
	topic := pr.Packet.Topic
	// Topic format: jarvis/<device>/<suffix>
	parts := strings.SplitN(topic, "/", 3)
	if len(parts) != 3 || parts[0] != "jarvis" {
		return false, nil
	}
	device := parts[1]
	suffix := parts[2]

	switch suffix {
	case "wake_detected":
		log.Printf("[mqtt] %s: wake word detected", device)
	case "audio_start":
		v.router.StartSession(device)
	case "audio":
		v.router.AppendAudio(device, pr.Packet.Payload)
	case "audio_stop":
		v.router.StopSession(device)
	}
	return true, nil
}

// publishLED sends an LED state command back to the device.
func (v *VoiceMQTTClient) PublishLED(device, state string) {
	if v.cm == nil {
		return
	}
	topic := fmt.Sprintf("jarvis/%s/led", device)
	log.Printf("[mqtt] publishing LED %s → %s", topic, state)
	if _, err := v.cm.Publish(context.Background(), &paho.Publish{
		Topic:   topic,
		QoS:     0,
		Payload: []byte(state),
	}); err != nil {
		log.Printf("[mqtt] publish LED error: %v", err)
	}
}

// PublishStopStreaming tells the device to stop streaming audio.
func (v *VoiceMQTTClient) PublishStopStreaming(device string) {
	if v.cm == nil {
		return
	}
	topic := fmt.Sprintf("jarvis/%s/stop_streaming", device)
	log.Printf("[mqtt] publishing stop_streaming → %s", device)
	if _, err := v.cm.Publish(context.Background(), &paho.Publish{
		Topic:   topic,
		QoS:     0,
		Payload: []byte("stop"),
	}); err != nil {
		log.Printf("[mqtt] publish stop_streaming error: %v", err)
	}
}

// SignalError flashes the error LED for 2 seconds then turns it off.
func (v *VoiceMQTTClient) SignalError(device string) {
	v.PublishLED(device, "error")
	go func() {
		time.Sleep(2 * time.Second)
		v.PublishLED(device, "off")
	}()
}

// PublishTTSURL sends a TTS audio URL to a device for playback.
func (v *VoiceMQTTClient) PublishTTSURL(device, mediaURL string) error {
	_, err := v.cm.Publish(context.Background(), &paho.Publish{
		Topic:   fmt.Sprintf("jarvis/%s/tts_url", device),
		QoS:     0,
		Payload: []byte(mediaURL),
	})
	return err
}
