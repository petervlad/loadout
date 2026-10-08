package mail

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Queue limits.
const (
	queueSize    = 256
	sendTimeout  = 60 * time.Second
	drainTimeout = 15 * time.Second
)

// ErrQueueFull means mail is arriving faster than it can be sent.
var ErrQueueFull = errors.New("mail queue is full")

// ErrClosed means the queue was closed.
var ErrClosed = errors.New("mail queue is closed")

// Mailer delivers one message.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Queue sends messages through a Mailer in the background, one at a time.
// Send returns at once, so a request that sends a mail takes the same time
// whether or not it sent one. Failures are logged. Close stops the worker.
type Queue struct {
	next Mailer
	log  *slog.Logger

	mu     sync.Mutex
	closed bool
	ch     chan Message

	done   chan struct{}
	cancel context.CancelFunc

	// What the worker did, for the server page.
	statsMu     sync.Mutex
	sent        int
	failed      int
	lastFailure time.Time
}

// QueueStats is what a queue has done since it started.
type QueueStats struct {
	// Waiting is how many mails are queued and not sent yet.
	Waiting int
	Sent    int
	Failed  int
	// LastFailure is when a mail last failed to go out, zero if never.
	LastFailure time.Time
}

// Stats reports what the queue has done since it started.
func (q *Queue) Stats() QueueStats {
	q.statsMu.Lock()
	defer q.statsMu.Unlock()
	return QueueStats{Waiting: len(q.ch), Sent: q.sent, Failed: q.failed, LastFailure: q.lastFailure}
}

// NewQueue starts a queue in front of next. Call Close to stop it.
func NewQueue(next Mailer, log *slog.Logger) *Queue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{next: next, log: log, ch: make(chan Message, queueSize), done: make(chan struct{}), cancel: cancel}
	go q.work(ctx)
	return q
}

// Send queues m for delivery.
func (q *Queue) Send(_ context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	select {
	case q.ch <- m:
		return nil
	default:
		return ErrQueueFull
	}
}

func (q *Queue) work(ctx context.Context) {
	defer close(q.done)
	for m := range q.ch {
		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := q.next.Send(sctx, m)
		cancel()
		q.statsMu.Lock()
		if err != nil {
			q.failed++
			q.lastFailure = time.Now()
		} else {
			q.sent++
		}
		q.statsMu.Unlock()
		if err != nil {
			q.log.Error("send login email failed", "to", Redact(m.To), "err", err)
		}
	}
}

// Close stops accepting mail, sends what is queued for up to 15 seconds,
// then gives up on the rest.
func (q *Queue) Close() error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	select {
	case <-q.done:
	case <-timer.C:
		q.cancel()
		<-q.done
	}
	q.cancel()
	return nil
}
