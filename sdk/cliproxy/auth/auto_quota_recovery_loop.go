package auth

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

type quotaRecoveryLoop struct {
	manager  *Manager
	interval time.Duration

	mu    sync.Mutex
	queue quotaRecoveryMinHeap
	index map[string]*quotaRecoveryHeapItem

	wakeCh chan struct{}
}

type quotaRecoveryDueItem struct {
	id        string
	recoverAt time.Time
}

func newQuotaRecoveryLoop(manager *Manager, interval time.Duration) *quotaRecoveryLoop {
	if interval <= 0 {
		interval = quotaRecoveryCheckInterval
	}
	return &quotaRecoveryLoop{
		manager:  manager,
		interval: interval,
		index:    make(map[string]*quotaRecoveryHeapItem),
		wakeCh:   make(chan struct{}, 1),
	}
}

func (l *quotaRecoveryLoop) rebuild() {
	if l == nil || l.manager == nil {
		return
	}
	type entry struct {
		id        string
		recoverAt time.Time
	}
	entries := make([]entry, 0)

	l.manager.mu.RLock()
	for id, auth := range l.manager.auths {
		if recoverAt, ok := autoQuotaRecoveryAt(auth); ok {
			entries = append(entries, entry{id: id, recoverAt: recoverAt})
		}
	}
	l.manager.mu.RUnlock()

	l.mu.Lock()
	l.queue = l.queue[:0]
	l.index = make(map[string]*quotaRecoveryHeapItem, len(entries))
	for _, e := range entries {
		if e.recoverAt.IsZero() {
			continue
		}
		item := &quotaRecoveryHeapItem{id: e.id, recoverAt: e.recoverAt}
		heap.Push(&l.queue, item)
		l.index[e.id] = item
	}
	l.mu.Unlock()
	l.wake()
}

func (l *quotaRecoveryLoop) run(ctx context.Context) {
	if l == nil || l.manager == nil {
		return
	}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	var timerCh <-chan time.Time
	l.resetTimer(timer, &timerCh, time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.wakeCh:
			l.resetTimer(timer, &timerCh, time.Now())
		case <-timerCh:
			now := time.Now()
			l.handleDue(ctx, now)
			l.resetTimer(timer, &timerCh, now)
		}
	}
}

func (l *quotaRecoveryLoop) resetTimer(timer *time.Timer, timerCh *<-chan time.Time, now time.Time) {
	next, ok := l.peek()
	if !ok {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		*timerCh = nil
		return
	}
	wait := next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	if l.interval > 0 && wait > l.interval {
		wait = l.interval
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(wait)
	*timerCh = timer.C
}

func (l *quotaRecoveryLoop) peek() (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return time.Time{}, false
	}
	return l.queue[0].recoverAt, true
}

func (l *quotaRecoveryLoop) handleDue(ctx context.Context, now time.Time) {
	due := l.popDue(now)
	for _, item := range due {
		l.manager.recoverAutoDisabledQuotaAuth(ctx, item.id, item.recoverAt)
		l.rescheduleFromAuth(item.id)
	}
}

func (l *quotaRecoveryLoop) popDue(now time.Time) []quotaRecoveryDueItem {
	l.mu.Lock()
	defer l.mu.Unlock()

	var due []quotaRecoveryDueItem
	for len(l.queue) > 0 {
		item := l.queue[0]
		if item == nil || item.recoverAt.After(now) {
			break
		}
		popped := heap.Pop(&l.queue).(*quotaRecoveryHeapItem)
		if popped == nil {
			continue
		}
		if current := l.index[popped.id]; current == popped {
			delete(l.index, popped.id)
		}
		due = append(due, quotaRecoveryDueItem{id: popped.id, recoverAt: popped.recoverAt})
	}
	return due
}

func (l *quotaRecoveryLoop) rescheduleFromAuth(authID string) {
	if l == nil || l.manager == nil || authID == "" {
		return
	}
	l.manager.mu.RLock()
	auth := l.manager.auths[authID]
	recoverAt, ok := autoQuotaRecoveryAt(auth)
	l.manager.mu.RUnlock()
	if !ok {
		l.remove(authID)
		return
	}
	l.upsert(authID, recoverAt)
}

func (l *quotaRecoveryLoop) upsert(authID string, recoverAt time.Time) {
	if l == nil || authID == "" || recoverAt.IsZero() {
		return
	}
	l.mu.Lock()
	if item, ok := l.index[authID]; ok && item != nil {
		item.recoverAt = recoverAt
		heap.Fix(&l.queue, item.index)
		l.mu.Unlock()
		l.wake()
		return
	}
	item := &quotaRecoveryHeapItem{id: authID, recoverAt: recoverAt}
	heap.Push(&l.queue, item)
	l.index[authID] = item
	l.mu.Unlock()
	l.wake()
}

func (l *quotaRecoveryLoop) remove(authID string) {
	if l == nil || authID == "" {
		return
	}
	l.mu.Lock()
	item, ok := l.index[authID]
	if !ok || item == nil {
		l.mu.Unlock()
		return
	}
	heap.Remove(&l.queue, item.index)
	delete(l.index, authID)
	l.mu.Unlock()
	l.wake()
}

func (l *quotaRecoveryLoop) wake() {
	if l == nil {
		return
	}
	select {
	case l.wakeCh <- struct{}{}:
	default:
	}
}

type quotaRecoveryHeapItem struct {
	id        string
	recoverAt time.Time
	index     int
}

type quotaRecoveryMinHeap []*quotaRecoveryHeapItem

func (h quotaRecoveryMinHeap) Len() int { return len(h) }

func (h quotaRecoveryMinHeap) Less(i, j int) bool {
	return h[i].recoverAt.Before(h[j].recoverAt)
}

func (h quotaRecoveryMinHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *quotaRecoveryMinHeap) Push(x any) {
	item, ok := x.(*quotaRecoveryHeapItem)
	if !ok || item == nil {
		return
	}
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *quotaRecoveryMinHeap) Pop() any {
	old := *h
	n := len(old)
	if n == 0 {
		return (*quotaRecoveryHeapItem)(nil)
	}
	item := old[n-1]
	item.index = -1
	*h = old[:n-1]
	return item
}
