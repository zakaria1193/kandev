package providers

// fork(slack-notify): in-memory index of posted Slack messages.
//
// It maps a Kandev occurrence (for a clarification: the pending id) to the
// Slack channel + ts of the message that announced it, and back. It is
// bounded and process-local on purpose: persisting it would need a schema
// change, and a thread-reply integration only needs recent questions. Records
// are also logged, so a restart loses nothing that cannot be recovered.

import "sync"

const slackThreadIndexCapacity = 2048

// SlackThreadIndex is a bounded FIFO of SlackPostRecords.
type SlackThreadIndex struct {
	mu           sync.RWMutex
	capacity     int
	order        []string
	byOccurrence map[string]SlackPostRecord
	byThread     map[string]string
}

// NewSlackThreadIndex returns an empty index holding at most capacity records.
func NewSlackThreadIndex(capacity int) *SlackThreadIndex {
	if capacity <= 0 {
		capacity = slackThreadIndexCapacity
	}
	return &SlackThreadIndex{
		capacity:     capacity,
		byOccurrence: map[string]SlackPostRecord{},
		byThread:     map[string]string{},
	}
}

func slackOccurrenceKey(eventType, occurrenceID string) string {
	return eventType + "|" + occurrenceID
}

func slackThreadKey(channel, ts string) string {
	return channel + "|" + ts
}

// Add stores a record, evicting the oldest one when full.
func (i *SlackThreadIndex) Add(record SlackPostRecord) {
	if record.OccurrenceID == "" || record.TS == "" {
		return
	}
	key := slackOccurrenceKey(record.EventType, record.OccurrenceID)
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, exists := i.byOccurrence[key]; !exists {
		i.order = append(i.order, key)
	}
	i.byOccurrence[key] = record
	i.byThread[slackThreadKey(record.Channel, record.TS)] = key
	for len(i.order) > i.capacity {
		oldest := i.order[0]
		i.order = i.order[1:]
		if evicted, ok := i.byOccurrence[oldest]; ok {
			delete(i.byThread, slackThreadKey(evicted.Channel, evicted.TS))
			delete(i.byOccurrence, oldest)
		}
	}
}

// ByOccurrence returns the Slack message posted for an occurrence.
func (i *SlackThreadIndex) ByOccurrence(eventType, occurrenceID string) (SlackPostRecord, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	record, ok := i.byOccurrence[slackOccurrenceKey(eventType, occurrenceID)]
	return record, ok
}

// ByThread returns the occurrence a Slack thread (channel + parent ts) was
// started for — the lookup a thread reply needs.
func (i *SlackThreadIndex) ByThread(channel, ts string) (SlackPostRecord, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	key, ok := i.byThread[slackThreadKey(channel, ts)]
	if !ok {
		return SlackPostRecord{}, false
	}
	record, ok := i.byOccurrence[key]
	return record, ok
}
