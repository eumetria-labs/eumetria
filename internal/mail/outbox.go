package mail

import (
	"context"
	"log"
	"sync"
	"time"
)

// Outbox sends messages in the background, so a request never waits for a
// mail server: a few tries with pauses between them, then it gives up and
// says so in the log and in Status. A restart forgets what was waiting;
// what the mails say is shown in the page anyway.
type Outbox struct {
	// Sender returns how to send now (the settings may have changed), or
	// nil when e-mail isn't set up.
	Sender func(ctx context.Context) Sender
	Tries  int             // default 3
	Pause  []time.Duration // before the 2nd, 3rd, … try; default 1 and 5 minutes

	once   sync.Once
	queue  chan Message
	urgent chan Message // security alerts: they go first

	mu     sync.Mutex
	status Status
}

// Status is how sending went lately, for the admin.
type Status struct {
	LastSent  time.Time `json:"last_sent,omitzero"`
	LastError string    `json:"last_error,omitempty"`
	ErrorAt   time.Time `json:"error_at,omitzero"`
	Waiting   int       `json:"waiting"`
}

// Start runs the outbox until ctx ends.
func (o *Outbox) Start(ctx context.Context) {
	o.init()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-o.urgent:
				o.deliver(ctx, m)
				continue
			default:
			}
			select {
			case <-ctx.Done():
				return
			case m := <-o.urgent:
				o.deliver(ctx, m)
			case m := <-o.queue:
				o.deliver(ctx, m)
			}
		}
	}()
}

func (o *Outbox) init() {
	o.once.Do(func() { o.queue, o.urgent = make(chan Message, 200), make(chan Message, 200) })
}

// Queue adds m; false when the queue is full (it is then dropped).
func (o *Outbox) Queue(m Message) bool {
	o.init()
	return o.put(o.queue, m)
}

// QueueUrgent adds m ahead of what Queue added: for security alerts.
func (o *Outbox) QueueUrgent(m Message) bool {
	o.init()
	return o.put(o.urgent, m)
}

func (o *Outbox) put(q chan Message, m Message) bool {
	select {
	case q <- m:
		return true
	default:
		log.Printf("mail: queue full, dropped a mail to %s", m.To)
		return false
	}
}

// Status says how sending went lately.
func (o *Outbox) Status() Status {
	o.init()
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.status
	s.Waiting = len(o.queue) + len(o.urgent)
	return s
}

func (o *Outbox) deliver(ctx context.Context, m Message) {
	tries := o.Tries
	if tries < 1 {
		tries = 3
	}
	pause := o.Pause
	if pause == nil {
		pause = []time.Duration{time.Minute, 5 * time.Minute}
	}
	var err error
	for i := range tries {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pause[min(i-1, len(pause)-1)]):
			}
		}
		s := o.Sender(ctx)
		if s == nil {
			log.Printf("mail: not set up any more; dropped a mail to %s", m.To)
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err = s.Send(sendCtx, m)
		cancel()
		if err == nil {
			// The server took it; what happens after that is between it and
			// the recipient's (a bounce goes to the sender address).
			log.Printf("mail: sent %q to %s", m.Subject, m.To)
			o.mu.Lock()
			o.status.LastSent = time.Now()
			o.mu.Unlock()
			return
		}
		log.Printf("mail: to %s, try %d: %v", m.To, i+1, err)
	}
	log.Printf("mail: gave up on %q to %s after %d tries: %v", m.Subject, m.To, tries, err)
	o.mu.Lock()
	o.status.LastError, o.status.ErrorAt = Category(err), time.Now()
	o.mu.Unlock()
}

// Send sends m now, without the queue: for a test mail, whose result the
// admin waits for.
func (o *Outbox) Send(ctx context.Context, m Message) error {
	s := o.Sender(ctx)
	if s == nil {
		return ErrNotSetUp
	}
	err := s.Send(ctx, m)
	o.mu.Lock()
	if err == nil {
		o.status.LastSent = time.Now()
	} else {
		o.status.LastError, o.status.ErrorAt = Category(err), time.Now()
	}
	o.mu.Unlock()
	return err
}

var ErrNotSetUp = errNotSetUp{}

type errNotSetUp struct{}

func (errNotSetUp) Error() string { return "e-mail isn't set up" }
