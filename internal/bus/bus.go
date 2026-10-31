// Package bus wraps the paho MQTT client: one connection that subscribes to
// Solar Assistant and publishes deadzone/* plus Home Assistant discovery.
//
// Reconnect is automatic; on every (re)connect the bus re-subscribes, marks
// itself online on the availability topic and republishes discovery so a
// restarted broker or Home Assistant picks the entities back up.
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/model"
)

// Handler receives one message.
type Handler func(topic string, payload []byte)

// Client is the MQTT bus.
type Client struct {
	cfg     config.MQTT
	version string
	log     *slog.Logger
	topics  Topics

	mu   sync.Mutex
	c    mqtt.Client
	subs map[string]Handler
}

// New prepares a client; call Connect to dial.
func New(cfg config.MQTT, version string, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{cfg: cfg, version: version, log: log, topics: Topics{Prefix: cfg.TopicPrefix}, subs: map[string]Handler{}}
}

// Topics exposes the topic layout.
func (c *Client) Topics() Topics { return c.topics }

func (c *Client) options() *mqtt.ClientOptions {
	o := mqtt.NewClientOptions().
		AddBroker(c.cfg.BrokerURL()).
		SetClientID(c.cfg.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetMaxReconnectInterval(30*time.Second).
		SetKeepAlive(30*time.Second).
		SetConnectTimeout(10*time.Second).
		SetOrderMatters(false).
		SetWill(c.topics.Availability(), "offline", 1, true)
	if c.cfg.User != "" {
		o.SetUsername(c.cfg.User)
		o.SetPassword(c.cfg.Pass)
	}
	o.SetOnConnectHandler(c.onConnect)
	o.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		c.log.Warn("mqtt connection lost", "err", err)
	})
	return o
}

// Connect dials the broker and waits until connected or ctx expires. On
// timeout the client keeps retrying in the background and the error is
// advisory; callers that need a hard answer (check) should Close.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.c == nil {
		c.c = mqtt.NewClient(c.options())
	}
	cl := c.c
	c.mu.Unlock()
	tok := cl.Connect()
	done := make(chan struct{})
	go func() { tok.Wait(); close(done) }()
	select {
	case <-done:
		return tok.Error()
	case <-ctx.Done():
		if cl.IsConnectionOpen() {
			return nil
		}
		return fmt.Errorf("mqtt: not connected to %s yet: %w", c.cfg.BrokerURL(), ctx.Err())
	}
}

func (c *Client) onConnect(cl mqtt.Client) {
	c.log.Info("mqtt connected", "broker", c.cfg.BrokerURL())
	c.mu.Lock()
	subs := make(map[string]Handler, len(c.subs))
	for k, v := range c.subs {
		subs[k] = v
	}
	c.mu.Unlock()
	for topic, h := range subs {
		c.subscribe(cl, topic, h)
	}
	if tok := cl.Publish(c.topics.Availability(), 1, true, "online"); tok.Wait() && tok.Error() != nil {
		c.log.Warn("availability publish failed", "err", tok.Error())
	}
	if c.cfg.HADiscovery {
		if err := c.PublishDiscovery(); err != nil {
			c.log.Warn("discovery publish failed", "err", err)
		}
	}
}

func (c *Client) subscribe(cl mqtt.Client, topic string, h Handler) {
	tok := cl.Subscribe(topic, 0, func(_ mqtt.Client, m mqtt.Message) {
		h(m.Topic(), m.Payload())
	})
	if tok.Wait() && tok.Error() != nil {
		c.log.Warn("subscribe failed", "topic", topic, "err", tok.Error())
		return
	}
	c.log.Debug("subscribed", "topic", topic)
}

// Subscribe registers a handler; it is applied now if connected and again
// after every reconnect.
func (c *Client) Subscribe(topic string, h func(topic string, payload []byte)) error {
	if topic == "" || h == nil {
		return errors.New("mqtt: empty subscription")
	}
	c.mu.Lock()
	c.subs[topic] = h
	cl := c.c
	c.mu.Unlock()
	if cl != nil && cl.IsConnectionOpen() {
		c.subscribe(cl, topic, h)
	}
	return nil
}

// Publish sends one message at QoS 0.
func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	c.mu.Lock()
	cl := c.c
	c.mu.Unlock()
	if cl == nil || !cl.IsConnectionOpen() {
		return fmt.Errorf("mqtt: not connected (%s)", topic)
	}
	tok := cl.Publish(topic, 0, retain, payload)
	if tok.WaitTimeout(5*time.Second) && tok.Error() != nil {
		return tok.Error()
	}
	return nil
}

// PublishAll sends a batch, returning the first error.
func (c *Client) PublishAll(msgs []Message) error {
	for _, m := range msgs {
		if err := c.Publish(m.Topic, m.Payload, m.Retain); err != nil {
			return err
		}
	}
	return nil
}

// PublishState publishes the full topic set for a state.
func (c *Client) PublishState(st model.State) error {
	msgs, err := StateMessages(c.cfg.TopicPrefix, st)
	if err != nil {
		return err
	}
	return c.PublishAll(msgs)
}

// PublishDiscovery (re)publishes the HA discovery configs.
func (c *Client) PublishDiscovery() error {
	msgs, err := DiscoveryMessages(c.cfg.HAPrefix, c.cfg.TopicPrefix, c.version)
	if err != nil {
		return err
	}
	return c.PublishAll(msgs)
}

// Connected reports the live connection state.
func (c *Client) Connected() bool {
	c.mu.Lock()
	cl := c.c
	c.mu.Unlock()
	return cl != nil && cl.IsConnectionOpen()
}

// Close publishes offline and disconnects.
func (c *Client) Close() {
	c.mu.Lock()
	cl := c.c
	c.c = nil
	c.mu.Unlock()
	if cl == nil {
		return
	}
	if cl.IsConnectionOpen() {
		tok := cl.Publish(c.topics.Availability(), 1, true, "offline")
		tok.WaitTimeout(2 * time.Second)
	}
	cl.Disconnect(250)
}
