package events

import (
	"sync"
	"time"
)

// Handler is a function that receives events.
type Handler func(Event)

// Bus is a simple pub/sub event bus with per-event-type cooldown deduplication.
type Bus struct {
	mu       sync.Mutex
	handlers []Handler
	cooldown map[EventType]time.Duration
	lastAt   map[EventType]time.Time
}

var defaultCooldowns = map[EventType]time.Duration{
	EvHighCPU:    5 * time.Minute,
	EvHighMemory: 5 * time.Minute,
	EvIfaceDown:  30 * time.Second,
	EvIfaceUp:    30 * time.Second,
}

func NewBus(cooldownSecs int) *Bus {
	cd := make(map[EventType]time.Duration, len(defaultCooldowns))
	for k, v := range defaultCooldowns {
		cd[k] = v
	}
	// override alert cooldowns from config
	if cooldownSecs > 0 {
		d := time.Duration(cooldownSecs) * time.Second
		cd[EvHighCPU] = d
		cd[EvHighMemory] = d
	}
	return &Bus{
		cooldown: cd,
		lastAt:   make(map[EventType]time.Time),
	}
}

func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	if cd, ok := b.cooldown[ev.Type]; ok {
		if last, ok2 := b.lastAt[ev.Type]; ok2 {
			if time.Since(last) < cd {
				b.mu.Unlock()
				return // still in cooldown
			}
		}
		b.lastAt[ev.Type] = time.Now()
	}
	handlers := b.handlers
	b.mu.Unlock()

	for _, h := range handlers {
		h(ev)
	}
}
