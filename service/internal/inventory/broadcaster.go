package inventory

import (
	"sync"

	"github.com/uberswe/tesseract/internal/protocol"
)

type Connection interface {
	Send(data []byte) bool
	ServerName() string
}

type Broadcaster struct {
	mu   sync.RWMutex
	subs map[protocol.UUID]map[Connection]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[protocol.UUID]map[Connection]struct{}),
	}
}

func (b *Broadcaster) Subscribe(uuid protocol.UUID, conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[uuid] == nil {
		b.subs[uuid] = make(map[Connection]struct{})
	}
	b.subs[uuid][conn] = struct{}{}
}

func (b *Broadcaster) Unsubscribe(uuid protocol.UUID, conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.subs[uuid]; s != nil {
		delete(s, conn)
		if len(s) == 0 {
			delete(b.subs, uuid)
		}
	}
}

func (b *Broadcaster) RemoveConnection(conn Connection) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for uuid, s := range b.subs {
		delete(s, conn)
		if len(s) == 0 {
			delete(b.subs, uuid)
		}
	}
}

func (b *Broadcaster) Broadcast(uuid protocol.UUID, data []byte, exclude Connection) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if s := b.subs[uuid]; s != nil {
		for conn := range s {
			if conn == exclude {
				continue
			}
			conn.Send(data)
		}
	}
}

func (b *Broadcaster) HasSubscribers(uuid protocol.UUID) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[uuid]) > 0
}
