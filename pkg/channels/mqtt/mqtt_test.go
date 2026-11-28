package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

type fakeMQTTToken struct {
	err error
}

func (t fakeMQTTToken) Wait() bool                     { return true }
func (t fakeMQTTToken) WaitTimeout(time.Duration) bool { return true }
func (t fakeMQTTToken) Done() <-chan struct{}          { return nil }
func (t fakeMQTTToken) Error() error                   { return t.err }

type fakeMQTTMessage struct {
	pahomqtt.Message
	topic   string
	payload []byte
}

func (m *fakeMQTTMessage) Topic() string   { return m.topic }
func (m *fakeMQTTMessage) Payload() []byte { return m.payload }

type fakeMQTTClient struct {
	pahomqtt.Client

	subscribeTopic   string
	subscribeQoS     byte
	subscribeHandler pahomqtt.MessageHandler
	subscribeErr     error

	publishTopic   string
	publishQoS     byte
	publishRetain  bool
	publishPayload []byte
	publishErr     error

	disconnectCalls   int
	disconnectQuiesce uint
}

func (c *fakeMQTTClient) Subscribe(topic string, qos byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.subscribeTopic = topic
	c.subscribeQoS = qos
	c.subscribeHandler = handler
	return fakeMQTTToken{err: c.subscribeErr}
}

func (c *fakeMQTTClient) Publish(topic string, qos byte, retain bool, payload any) pahomqtt.Token {
	c.publishTopic = topic
	c.publishQoS = qos
	c.publishRetain = retain
	c.publishPayload, _ = payload.([]byte)
	return fakeMQTTToken{err: c.publishErr}
}

func (c *fakeMQTTClient) Disconnect(quiesce uint) {
	c.disconnectCalls++
	c.disconnectQuiesce = quiesce
}

func newTestMQTTChannel(t *testing.T, allowFrom ...string) (*MQTTChannel, *bus.MessageBus) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	ch, err := NewMQTTChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.MQTTSettings{
			Broker:      "tcp://127.0.0.1:1883",
			AgentID:     "agent",
			TopicPrefix: "picoclaw/",
			ClientID:    "test-client",
			QoS:         1,
		},
		msgBus,
	)
	if err != nil {
		t.Fatalf("NewMQTTChannel() error = %v", err)
	}
	return ch, msgBus
}

func receiveMQTTInbound(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound MQTT message")
		return bus.InboundMessage{}
	}
}

func assertNoMQTTInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound MQTT message: %+v", msg)
	default:
	}
}

func TestNewMQTTChannelValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.MQTTSettings
		wantErr string
	}{
		{
			name:    "missing broker",
			cfg:     &config.MQTTSettings{AgentID: "agent"},
			wantErr: "mqtt broker is required",
		},
		{
			name:    "missing agent ID",
			cfg:     &config.MQTTSettings{Broker: "tcp://127.0.0.1:1883"},
			wantErr: "mqtt agent_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := NewMQTTChannel(&config.Channel{}, tt.cfg, bus.NewMessageBus())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewMQTTChannel() error = %v, want containing %q", err, tt.wantErr)
			}
			if ch != nil {
				t.Fatalf("NewMQTTChannel() channel = %v, want nil", ch)
			}
		})
	}
}

func TestNewMQTTChannelConfiguration(t *testing.T) {
	t.Run("explicit client ID and channel options", func(t *testing.T) {
		cfg := &config.MQTTSettings{
			Broker:   "tcp://127.0.0.1:1883",
			AgentID:  "agent",
			ClientID: "configured-client",
			QoS:      2,
		}
		bc := &config.Channel{ReasoningChannelID: "reasoning"}
		ch, err := NewMQTTChannel(bc, cfg, bus.NewMessageBus())
		if err != nil {
			t.Fatalf("NewMQTTChannel() error = %v", err)
		}
		if ch.Name() != "mqtt" {
			t.Errorf("Name() = %q, want mqtt", ch.Name())
		}
		if ch.clientID != cfg.ClientID {
			t.Errorf("clientID = %q, want %q", ch.clientID, cfg.ClientID)
		}
		if ch.qos != 2 {
			t.Errorf("qos = %d, want 2", ch.qos)
		}
		if ch.ReasoningChannelID() != bc.ReasoningChannelID {
			t.Errorf("ReasoningChannelID() = %q, want %q", ch.ReasoningChannelID(), bc.ReasoningChannelID)
		}
		if ch.IsRunning() {
			t.Error("new channel is running")
		}
	})

	t.Run("generated client ID", func(t *testing.T) {
		ch, err := NewMQTTChannel(&config.Channel{}, &config.MQTTSettings{
			Broker:  "tcp://127.0.0.1:1883",
			AgentID: "agent",
		}, bus.NewMessageBus())
		if err != nil {
			t.Fatalf("NewMQTTChannel() error = %v", err)
		}
		if !regexp.MustCompile(`^picoclaw-mqtt-agent-[0-9a-f]{8}$`).MatchString(ch.clientID) {
			t.Errorf("generated clientID = %q, want picoclaw-mqtt-agent-<8 hex chars>", ch.clientID)
		}
	})
}

func TestMQTTChannelTopicPrefix(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{prefix: "", want: "/picoclaw"},
		{prefix: "///", want: "/picoclaw"},
		{prefix: "picoclaw", want: "picoclaw"},
		{prefix: "picoclaw///", want: "picoclaw"},
		{prefix: "/picoclaw/", want: "/picoclaw"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			ch, _ := newTestMQTTChannel(t)
			ch.cfg.TopicPrefix = tt.prefix
			if got := ch.topicPrefix(); got != tt.want {
				t.Errorf("topicPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMQTTChannelClientIDFromTopic(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		topic  string
		want   string
		ok     bool
	}{
		{name: "request", prefix: "picoclaw/", topic: "picoclaw/agent/device-1/request", want: "device-1", ok: true},
		{
			name:   "leading slash",
			prefix: "/picoclaw/",
			topic:  "/picoclaw/agent/device-1/request",
			want:   "device-1",
			ok:     true,
		},
		{name: "default prefix", topic: "/picoclaw/agent/device-1/request", want: "device-1", ok: true},
		{name: "wrong prefix", topic: "other/agent/device-1/request"},
		{name: "wrong agent", topic: "picoclaw/other/device-1/request"},
		{name: "missing client separator", topic: "picoclaw/agent/device-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, _ := newTestMQTTChannel(t)
			ch.cfg.TopicPrefix = tt.prefix
			got, ok := ch.clientIDFromTopic(tt.topic)
			if got != tt.want || ok != tt.ok {
				t.Errorf("clientIDFromTopic(%q) = (%q, %v), want (%q, %v)", tt.topic, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestMQTTChannelSubscribe(t *testing.T) {
	t.Run("success installs inbound handler", func(t *testing.T) {
		ch, msgBus := newTestMQTTChannel(t)
		client := &fakeMQTTClient{}
		if err := ch.subscribe(client); err != nil {
			t.Fatalf("subscribe() error = %v", err)
		}
		if client.subscribeTopic != "picoclaw/agent/+/request" {
			t.Errorf("subscribe topic = %q, want picoclaw/agent/+/request", client.subscribeTopic)
		}
		if client.subscribeQoS != 1 {
			t.Errorf("subscribe QoS = %d, want 1", client.subscribeQoS)
		}
		if client.subscribeHandler == nil {
			t.Fatal("subscribe handler is nil")
		}

		client.subscribeHandler(client, &fakeMQTTMessage{
			topic:   "picoclaw/agent/device-1/request",
			payload: []byte(`{"text":"hello"}`),
		})
		msg := receiveMQTTInbound(t, msgBus)
		if msg.Content != "hello" || msg.ChatID != "mqtt:device-1" {
			t.Errorf("inbound message = %+v, want hello for mqtt:device-1", msg)
		}
	})

	t.Run("broker error", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		wantErr := errors.New("subscription rejected")
		client := &fakeMQTTClient{subscribeErr: wantErr}
		if err := ch.subscribe(client); !errors.Is(err, wantErr) {
			t.Fatalf("subscribe() error = %v, want %v", err, wantErr)
		}
	})
}

func TestMQTTChannelHandleInboundAllowedMessage(t *testing.T) {
	ch, msgBus := newTestMQTTChannel(t, "device-1")
	ch.handleInbound(&fakeMQTTMessage{
		topic:   "picoclaw/agent/device-1/request",
		payload: []byte(`{"text":"turn on the light"}`),
	})

	msg := receiveMQTTInbound(t, msgBus)
	if msg.Content != "turn on the light" {
		t.Errorf("Content = %q, want turn on the light", msg.Content)
	}
	if msg.Channel != "mqtt" || msg.Context.Channel != "mqtt" {
		t.Errorf("Channel = %q, Context.Channel = %q, want mqtt", msg.Channel, msg.Context.Channel)
	}
	if msg.ChatID != "mqtt:device-1" || msg.Context.ChatID != "mqtt:device-1" {
		t.Errorf("ChatID = %q, Context.ChatID = %q, want mqtt:device-1", msg.ChatID, msg.Context.ChatID)
	}
	if msg.Context.ChatType != "direct" {
		t.Errorf("ChatType = %q, want direct", msg.Context.ChatType)
	}
	if msg.SenderID != "device-1" || msg.Context.SenderID != "device-1" {
		t.Errorf("SenderID = %q, Context.SenderID = %q, want device-1", msg.SenderID, msg.Context.SenderID)
	}
	if len(msg.Media) != 0 {
		t.Errorf("Media = %v, want none", msg.Media)
	}
}

func TestMQTTChannelHandleInboundRejectsInvalidMessages(t *testing.T) {
	tests := []struct {
		name      string
		topic     string
		payload   string
		allowFrom []string
	}{
		{name: "wrong topic prefix", topic: "other/agent/device-1/request", payload: `{"text":"hello"}`},
		{name: "wrong agent", topic: "picoclaw/other/device-1/request", payload: `{"text":"hello"}`},
		{name: "missing client separator", topic: "picoclaw/agent/device-1", payload: `{"text":"hello"}`},
		{name: "malformed JSON", topic: "picoclaw/agent/device-1/request", payload: `{"text":`},
		{name: "empty payload", topic: "picoclaw/agent/device-1/request"},
		{name: "null payload", topic: "picoclaw/agent/device-1/request", payload: `null`},
		{name: "missing text", topic: "picoclaw/agent/device-1/request", payload: `{}`},
		{name: "empty text", topic: "picoclaw/agent/device-1/request", payload: `{"text":""}`},
		{name: "non-string text", topic: "picoclaw/agent/device-1/request", payload: `{"text":42}`},
		{
			name:      "disallowed client",
			topic:     "picoclaw/agent/device-2/request",
			payload:   `{"text":"hello"}`,
			allowFrom: []string{"device-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, msgBus := newTestMQTTChannel(t, tt.allowFrom...)
			ch.handleInbound(&fakeMQTTMessage{topic: tt.topic, payload: []byte(tt.payload)})
			assertNoMQTTInbound(t, msgBus)
		})
	}
}

func TestMQTTChannelSend(t *testing.T) {
	t.Run("not running", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		client := &fakeMQTTClient{}
		ch.client = client
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "mqtt:device-1", Content: "hello"})
		if !errors.Is(err, channels.ErrNotRunning) {
			t.Fatalf("Send() error = %v, want ErrNotRunning", err)
		}
		if got != nil || client.publishTopic != "" {
			t.Errorf("Send() = (%v, %q), want no result or publish", got, client.publishTopic)
		}
	})

	t.Run("blank content", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		client := &fakeMQTTClient{}
		ch.client = client
		for _, content := range []string{"", " \t\n"} {
			got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "mqtt:device-1", Content: content})
			if err != nil || got != nil {
				t.Errorf("Send() = (%v, %v), want (nil, nil)", got, err)
			}
		}
		if client.publishTopic != "" {
			t.Errorf("blank content published to %q", client.publishTopic)
		}
	})

	t.Run("invalid chat ID", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		client := &fakeMQTTClient{}
		ch.client = client
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "device-1", Content: "hello"})
		if err != nil || got != nil {
			t.Errorf("Send() = (%v, %v), want (nil, nil)", got, err)
		}
		if client.publishTopic != "" {
			t.Errorf("invalid chat ID published to %q", client.publishTopic)
		}
	})

	t.Run("publishes JSON response", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		client := &fakeMQTTClient{}
		ch.client = client
		content := "hello \"MQTT\"\nsecond line"
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "mqtt:device-1", Content: content})
		if err != nil || got != nil {
			t.Fatalf("Send() = (%v, %v), want (nil, nil)", got, err)
		}
		if client.publishTopic != "picoclaw/agent/device-1/response" {
			t.Errorf("publish topic = %q, want picoclaw/agent/device-1/response", client.publishTopic)
		}
		if client.publishQoS != 1 || client.publishRetain {
			t.Errorf("publish QoS/retain = (%d, %v), want (1, false)", client.publishQoS, client.publishRetain)
		}
		var payload mqttPayload
		if err := json.Unmarshal(client.publishPayload, &payload); err != nil {
			t.Fatalf("published payload is invalid JSON: %v", err)
		}
		if payload.Text != content {
			t.Errorf("published text = %q, want %q", payload.Text, content)
		}
	})

	t.Run("publish error", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		wantErr := errors.New("broker unavailable")
		ch.client = &fakeMQTTClient{publishErr: wantErr}
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "mqtt:device-1", Content: "hello"})
		if got != nil || !errors.Is(err, wantErr) {
			t.Fatalf("Send() = (%v, %v), want (nil, wrapped %v)", got, err, wantErr)
		}
		if !strings.Contains(err.Error(), "mqtt: publish failed") {
			t.Errorf("Send() error = %v, want publish context", err)
		}
	})
}

func TestMQTTChannelStop(t *testing.T) {
	t.Run("without client", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		if err := ch.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
		if ch.IsRunning() {
			t.Error("channel is still running after Stop()")
		}
	})

	t.Run("disconnects client", func(t *testing.T) {
		ch, _ := newTestMQTTChannel(t)
		ch.SetRunning(true)
		client := &fakeMQTTClient{}
		ch.client = client
		if err := ch.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
		if ch.IsRunning() {
			t.Error("channel is still running after Stop()")
		}
		if client.disconnectCalls != 1 || client.disconnectQuiesce != 500 {
			t.Errorf(
				"Disconnect() = (%d calls, %d ms), want (1, 500)",
				client.disconnectCalls,
				client.disconnectQuiesce,
			)
		}
	})
}
