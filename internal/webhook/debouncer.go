package webhook

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DebounceConfig holds debouncer configuration.
type DebounceConfig struct {
	// Interval is the debounce window duration.
	// Events within this window will be coalesced into a single event.
	Interval time.Duration
	// MaxWait is the maximum time to wait before dispatching.
	// Even if events keep coming, dispatch after this time.
	MaxWait time.Duration
}

// DefaultDebounceConfig returns default debounce configuration.
func DefaultDebounceConfig() DebounceConfig {
	return DebounceConfig{
		Interval: 1 * time.Second, // Coalesce events within 1 second
		MaxWait:  5 * time.Second, // Always dispatch within 5 seconds
	}
}

// pendingEvent tracks a debounced event.
type pendingEvent struct {
	event      *Event
	timer      *time.Timer
	firstSeen  time.Time
	lastUpdate time.Time
}

// Debouncer coalesces rapid-fire events into single deliveries.
// This is useful when the same entity (e.g., page) is updated multiple times
// in quick succession - we only want to send one webhook notification.
type Debouncer struct {
	dispatcher *Dispatcher
	config     DebounceConfig
	pending    map[string]*pendingEvent // key -> pending event
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewDebouncer creates a new event debouncer.
func NewDebouncer(dispatcher *Dispatcher, config DebounceConfig) *Debouncer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Debouncer{
		dispatcher: dispatcher,
		config:     config,
		pending:    make(map[string]*pendingEvent),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// eventKey generates a unique key for deduplicating events.
// The key is based on event type and entity ID (extracted from the event data).
func eventKey(event *Event) string {
	// Try to extract entity ID from common event data structures
	var entityID int64

	switch data := event.Data.(type) {
	case PageEventData:
		entityID = data.ID
	case *PageEventData:
		entityID = data.ID
	case MediaEventData:
		entityID = data.ID
	case *MediaEventData:
		entityID = data.ID
	case UserEventData:
		entityID = data.ID
	case *UserEventData:
		entityID = data.ID
	case FormEventData:
		entityID = data.SubmissionID
	case *FormEventData:
		entityID = data.SubmissionID
	case map[string]any:
		if id, ok := data["id"].(int64); ok {
			entityID = id
		} else if id, ok := data["id"].(float64); ok {
			entityID = int64(id)
		}
	default:
		// For unknown types, use the event type only (no entity-level deduplication)
		return event.Type
	}

	return fmt.Sprintf("%s:%d", event.Type, entityID)
}

// Dispatch queues an event for debounced delivery.
// If an event for the same entity is already pending, it will be updated
// with the latest data and the timer will be reset.
func (d *Debouncer) Dispatch(ctx context.Context, event *Event) error {
	key := eventKey(event)
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	if existing, ok := d.pending[key]; ok {
		// Update existing pending event with new data
		existing.event = event
		existing.lastUpdate = now

		// Check if we've exceeded max wait time
		if now.Sub(existing.firstSeen) >= d.config.MaxWait {
			// Dispatch immediately
			d.dispatchLocked(key)
			return nil
		}

		// Reset the timer
		existing.timer.Reset(d.config.Interval)
		d.dispatcher.logger.Debug("debounced event updated",
			"key", key,
			"event_type", event.Type,
			"wait_time", now.Sub(existing.firstSeen))
		return nil
	}

	// Create new pending event
	pe := &pendingEvent{
		event:      event,
		firstSeen:  now,
		lastUpdate: now,
	}

	// Create timer that will dispatch after the interval
	pe.timer = time.AfterFunc(d.config.Interval, func() {
		d.mu.Lock()
		d.dispatchLocked(key)
		d.mu.Unlock()
	})

	d.pending[key] = pe
	d.dispatcher.logger.Debug("debounced event queued",
		"key", key,
		"event_type", event.Type)

	return nil
}

// dispatchLocked dispatches a pending event. Must be called with lock held.
func (d *Debouncer) dispatchLocked(key string) {
	pe, ok := d.pending[key]
	if !ok {
		return
	}

	// Stop timer if still running
	pe.timer.Stop()

	// Remove from pending
	delete(d.pending, key)

	// Dispatch the event asynchronously
	d.wg.Add(1)
	go func(event *Event) {
		defer d.wg.Done()
		if err := d.dispatcher.Dispatch(d.ctx, event); err != nil {
			d.dispatcher.logger.Error("failed to dispatch debounced event",
				"error", err,
				"event_type", event.Type)
		}
	}(pe.event)
}

// Flush immediately dispatches all pending events.
// This is useful during shutdown to ensure no events are lost.
func (d *Debouncer) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for key := range d.pending {
		d.dispatchLocked(key)
	}
}

// Stop stops the debouncer and flushes all pending events.
func (d *Debouncer) Stop() {
	d.Flush()
	d.cancel()
	d.wg.Wait()
}

// PendingCount returns the number of pending events.
func (d *Debouncer) PendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

// DispatchEvent is a convenience method to dispatch an event with debouncing.
func (d *Debouncer) DispatchEvent(ctx context.Context, eventType string, data any) error {
	return d.Dispatch(ctx, NewEvent(eventType, data))
}
