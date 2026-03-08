package pulseboard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"
)

// WebhookNotifier returns a [StatusChange] callback that sends an HTTP POST
// to the given URL with the change serialised as JSON.
//
// The webhook fires after every status transition. Use [WithWebhookEventFilter]
// to restrict which transitions trigger a notification.
//
// Failed requests are logged via [slog.Default] but do not block polling.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithStatusChangeCallback(
//	        pulseboard.WebhookNotifier("https://hooks.slack.com/...",
//	            pulseboard.WithWebhookEventFilter("down", "degraded"),
//	        ),
//	    ),
//	)
func WebhookNotifier(url string, opts ...WebhookOption) func(StatusChange) {
	cfg := &webhookConfig{
		timeout: 10 * time.Second,
		headers: make(map[string]string),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Debounce state — only allocated when debounce is configured.
	var (
		dmu    sync.Mutex
		timers map[string]*time.Timer
	)
	if cfg.debounce > 0 {
		timers = make(map[string]*time.Timer)
	}

	send := func(change StatusChange) {
		payload, err := json.Marshal(change)
		if err != nil {
			slog.Default().Error("webhook: failed to marshal payload",
				"endpoint", change.EndpointName, "error", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			slog.Default().Error("webhook: failed to build request",
				"endpoint", change.EndpointName, "error", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		for k, v := range cfg.headers {
			req.Header.Set(k, v)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Default().Warn("webhook: request failed",
				"endpoint", change.EndpointName, "url", url, "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.Default().Warn("webhook: server returned non-2xx",
				"endpoint", change.EndpointName, "status_code", resp.StatusCode)
		}
	}

	return func(change StatusChange) {
		// Apply event filter.
		if len(cfg.eventFilter) > 0 {
			matched := false
			for _, event := range cfg.eventFilter {
				if Status(event) == change.CurrentStatus {
					matched = true
					break
				}
			}
			if !matched {
				return
			}
		}

		// Apply debounce.
		if cfg.debounce > 0 {
			captured := change
			dmu.Lock()
			if t, ok := timers[change.EndpointName]; ok {
				t.Stop()
			}
			timers[change.EndpointName] = time.AfterFunc(cfg.debounce, func() {
				send(captured)
			})
			dmu.Unlock()
			return
		}

		send(change)
	}
}

type webhookConfig struct {
	timeout     time.Duration
	eventFilter []string
	debounce    time.Duration
	headers     map[string]string
}

// WebhookOption configures a [WebhookNotifier].
type WebhookOption func(*webhookConfig)

// WithWebhookTimeout sets the HTTP request timeout. Default: 10 seconds.
func WithWebhookTimeout(d time.Duration) WebhookOption {
	return func(cfg *webhookConfig) { cfg.timeout = d }
}

// WithWebhookEventFilter restricts notifications to transitions where the current
// status matches one of the given values. Example: "down", "degraded".
// If not set, all transitions trigger the webhook.
func WithWebhookEventFilter(events ...string) WebhookOption {
	return func(cfg *webhookConfig) {
		cfg.eventFilter = append(cfg.eventFilter, events...)
	}
}

// WithWebhookHeaders adds custom HTTP headers to every webhook POST request.
// Useful for authorization: WithWebhookHeaders(map[string]string{"Authorization": "Bearer token"}).
func WithWebhookHeaders(headers map[string]string) WebhookOption {
	return func(cfg *webhookConfig) {
		maps.Copy(cfg.headers, headers)
	}
}

// WithWebhookDebounce sets the minimum duration a status must remain changed
// before the webhook fires. This prevents flap notifications.
// Debounce is per-endpoint; each endpoint's timer is independent.
func WithWebhookDebounce(d time.Duration) WebhookOption {
	return func(cfg *webhookConfig) { cfg.debounce = d }
}
