package ingress

import (
	"sync"

	"farmail/model"

	"github.com/google/uuid"
)

type emailSubscription struct {
	mailboxID uuid.UUID
	events    chan model.MailboxEmailEvent
}

func (s *Server) Subscribe(mailboxID uuid.UUID) (<-chan model.MailboxEmailEvent, func()) {
	s.eventMu.Lock()
	if s.eventSubscribers == nil {
		s.eventSubscribers = make(map[uint64]emailSubscription)
	}
	s.nextEventSubscriber++
	id := s.nextEventSubscriber
	channel := make(chan model.MailboxEmailEvent, 16)
	s.eventSubscribers[id] = emailSubscription{mailboxID: mailboxID, events: channel}
	s.eventMu.Unlock()

	var once sync.Once
	return channel, func() {
		once.Do(func() {
			s.eventMu.Lock()
			if subscription, ok := s.eventSubscribers[id]; ok {
				delete(s.eventSubscribers, id)
				close(subscription.events)
			}
			s.eventMu.Unlock()
		})
	}
}

func (s *Server) publishEmailEvent(event model.MailboxEmailEvent) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	for _, subscription := range s.eventSubscribers {
		if subscription.mailboxID != uuid.Nil && subscription.mailboxID != event.MailboxID {
			continue
		}
		select {
		case subscription.events <- event:
		default:
			s.eventsDropped.Add(1)
		}
	}
}

func (s *Server) eventSubscriberCount() int {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	return len(s.eventSubscribers)
}
