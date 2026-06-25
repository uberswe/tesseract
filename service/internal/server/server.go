package server

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/uberswe/tesseract/internal/db"
	"github.com/uberswe/tesseract/internal/inventory"
	"github.com/uberswe/tesseract/internal/protocol"
)

type Server struct {
	listener    net.Listener
	store       *inventory.Store
	broadcaster *inventory.Broadcaster
	persister   *db.Persister
	maxPacket   int
	pingInterval time.Duration

	mu    sync.Mutex
	conns map[*Connection]struct{}
}

func New(store *inventory.Store, broadcaster *inventory.Broadcaster, persister *db.Persister, maxPacket int, pingInterval time.Duration) *Server {
	return &Server{
		store:        store,
		broadcaster:  broadcaster,
		persister:    persister,
		maxPacket:    maxPacket,
		pingInterval: pingInterval,
		conns:        make(map[*Connection]struct{}),
	}
}

func (s *Server) Listen(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	slog.Info("TCP server listening", "addr", addr)

	go s.pingLoop(ctx)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("accept error", "error", err)
			continue
		}
		c := NewConnection(conn, s.maxPacket, s)
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		slog.Info("new connection", "remote", conn.RemoteAddr())
		go c.Run(ctx)
	}
}

func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) ConnectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) HandlePacket(conn *Connection, packetType byte, payload []byte) {
	switch packetType {
	case protocol.TypeHello:
		s.handleHello(conn, payload)
	case protocol.TypeSubscribe:
		s.handleSubscribe(conn, payload)
	case protocol.TypeUnsubscribe:
		s.handleUnsubscribe(conn, payload)
	case protocol.TypeInvPush:
		s.handleInvPush(conn, payload)
	case protocol.TypeBatchOps:
		s.handleBatchOps(conn, payload)
	case protocol.TypeInvRequest:
		s.handleInvRequest(conn, payload)
	case protocol.TypePong:
		// keepalive acknowledged
	case protocol.TypePing:
		s.handlePing(conn, payload)
	default:
		slog.Warn("unknown packet type", "type", packetType, "server", conn.ServerName())
	}
}

func (s *Server) OnDisconnect(conn *Connection) {
	slog.Info("connection lost", "server", conn.ServerName())
	s.broadcaster.RemoveConnection(conn)
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

func (s *Server) handleHello(conn *Connection, payload []byte) {
	name, version, err := protocol.ParseHelloVersion(payload)
	if err != nil {
		slog.Warn("invalid hello", "error", err)
		return
	}
	conn.SetServerName(name)
	conn.SetProtocolVersion(version)
	slog.Info("server identified", "name", name, "protocol", version)

	var buf bytes.Buffer
	protocol.WriteFrame(&buf, protocol.TypeHelloAck, protocol.MakeHelloAck(protocol.StatusOK))
	conn.Send(buf.Bytes())
}

func (s *Server) handleSubscribe(conn *Connection, payload []byte) {
	if len(payload) < 16 {
		return
	}
	uuid := protocol.ParseUUIDFromBytes(payload[0:16])
	s.broadcaster.Subscribe(uuid, conn)
	slog.Debug("subscribe", "uuid", uuid.String(), "server", conn.ServerName())
}

func (s *Server) handleUnsubscribe(conn *Connection, payload []byte) {
	if len(payload) < 16 {
		return
	}
	uuid := protocol.ParseUUIDFromBytes(payload[0:16])
	s.broadcaster.Unsubscribe(uuid, conn)
	slog.Debug("unsubscribe", "uuid", uuid.String(), "server", conn.ServerName())
}

func (s *Server) handleInvPush(conn *Connection, payload []byte) {
	uuid, expectedTs, nbtData, err := protocol.ParseInvPush(payload)
	if err != nil {
		slog.Warn("invalid inv_push", "error", err, "server", conn.ServerName())
		return
	}

	accepted, ts, storedData := s.store.Push(uuid, expectedTs, nbtData, conn.ServerName())

	if !accepted {
		var buf bytes.Buffer
		protocol.WriteFrame(&buf, protocol.TypeInvPushReject, protocol.MakeInvUpdate(uuid, ts, storedData))
		conn.Send(buf.Bytes())
		slog.Debug("inv_push rejected (stale)", "uuid", uuid.String(), "server", conn.ServerName(),
			"expected_ts", expectedTs, "actual_ts", ts)
		return
	}

	var ackBuf bytes.Buffer
	protocol.WriteFrame(&ackBuf, protocol.TypeInvPushAck, protocol.MakeInvPushAck(uuid, ts))
	conn.Send(ackBuf.Bytes())

	var buf bytes.Buffer
	protocol.WriteFrame(&buf, protocol.TypeInvUpdate, protocol.MakeInvUpdate(uuid, ts, storedData))
	s.broadcaster.Broadcast(uuid, buf.Bytes(), conn)

	slog.Debug("inv_push", "uuid", uuid.String(), "server", conn.ServerName(), "bytes", len(nbtData))
}

func (s *Server) handleInvRequest(conn *Connection, payload []byte) {
	if len(payload) < 16 {
		return
	}
	uuid := protocol.ParseUUIDFromBytes(payload[0:16])

	entry, ok := s.store.Get(uuid)
	if ok {
		var buf bytes.Buffer
		protocol.WriteFrame(&buf, protocol.TypeInvResponse, protocol.MakeInvResponse(uuid, true, entry.Timestamp, entry.Data))
		conn.Send(buf.Bytes())
		return
	}

	data, ts, err := s.persister.Load(context.Background(), uuid)
	if err != nil {
		slog.Debug("inventory not in DB", "uuid", uuid.String())
		var buf bytes.Buffer
		protocol.WriteFrame(&buf, protocol.TypeInvResponse, protocol.MakeInvResponse(uuid, false, 0, nil))
		conn.Send(buf.Bytes())
		return
	}

	s.store.Set(uuid, data, ts)
	var buf bytes.Buffer
	protocol.WriteFrame(&buf, protocol.TypeInvResponse, protocol.MakeInvResponse(uuid, true, ts, data))
	conn.Send(buf.Bytes())
}

func (s *Server) handlePing(conn *Connection, payload []byte) {
	if len(payload) < 8 {
		return
	}
	var buf bytes.Buffer
	protocol.WriteFrame(&buf, protocol.TypePong, payload[0:8])
	conn.Send(buf.Bytes())
}

func (s *Server) handleBatchOps(conn *Connection, payload []byte) {
	uuid, ops, err := protocol.ParseBatchOps(payload)
	if err != nil {
		slog.Warn("invalid batch_ops", "error", err, "server", conn.ServerName())
		return
	}

	results, ts := s.store.ProcessBatch(uuid, ops)

	snapshot, _, err := s.store.SnapshotFor(uuid)
	if err != nil {
		slog.Error("failed to snapshot after batch", "uuid", uuid.String(), "error", err)
		snapshot = nil
	}
	if snapshot == nil {
		snapshot = []byte{}
	}

	var resultBuf bytes.Buffer
	protocol.WriteFrame(&resultBuf, protocol.TypeBatchResult, protocol.MakeBatchResult(uuid, ts, results, snapshot))
	conn.Send(resultBuf.Bytes())

	if len(snapshot) > 0 {
		var updateBuf bytes.Buffer
		protocol.WriteFrame(&updateBuf, protocol.TypeInvUpdate, protocol.MakeInvUpdate(uuid, ts, snapshot))
		s.broadcaster.Broadcast(uuid, updateBuf.Bytes(), conn)
	}

	if time.Now().UnixMilli()%100 == 0 {
		totalItems, nonEmptySlots := s.store.GetStats(uuid)
		slog.Info("batch_ops_stats", "uuid", uuid.String(), "server", conn.ServerName(), "ops", len(ops),
			"total_items", totalItems, "non_empty_slots", nonEmptySlots)
	}
}

func (s *Server) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ts := time.Now().UnixMilli()
			var buf bytes.Buffer
			protocol.WriteFrame(&buf, protocol.TypePing, protocol.MakePing(ts))
			frame := buf.Bytes()

			s.mu.Lock()
			for c := range s.conns {
				c.Send(frame)
			}
			s.mu.Unlock()
		}
	}
}
