package server

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/uberswe/tesseract/internal/protocol"
)

type Connection struct {
	conn            net.Conn
	writer          *bufio.Writer
	writeMu         sync.Mutex
	writeCh         chan []byte
	serverName      string
	protocolVersion byte
	maxPacket       int
	cancel          context.CancelFunc
	handler         PacketHandler
}

type PacketHandler interface {
	HandlePacket(conn *Connection, packetType byte, payload []byte)
	OnDisconnect(conn *Connection)
}

func NewConnection(conn net.Conn, maxPacket int, handler PacketHandler) *Connection {
	return &Connection{
		conn:      conn,
		writer:    bufio.NewWriter(conn),
		writeCh:   make(chan []byte, 256),
		maxPacket: maxPacket,
		handler:   handler,
	}
}

func (c *Connection) ServerName() string {
	return c.serverName
}

func (c *Connection) SetServerName(name string) {
	c.serverName = name
}

func (c *Connection) ProtocolVersion() byte {
	return c.protocolVersion
}

func (c *Connection) SetProtocolVersion(v byte) {
	c.protocolVersion = v
}

func (c *Connection) Send(data []byte) bool {
	select {
	case c.writeCh <- data:
		return true
	default:
		slog.Warn("write channel full, dropping packet", "server", c.serverName)
		return false
	}
}

func (c *Connection) SendDirect(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Connection) Run(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	go c.writeLoop(ctx)
	c.readLoop(ctx)
}

func (c *Connection) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.conn.Close()
}

func (c *Connection) readLoop(ctx context.Context) {
	defer func() {
		c.handler.OnDisconnect(c)
		c.Close()
	}()

	reader := bufio.NewReader(c.conn)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		packetType, payload, err := protocol.ReadFrame(reader, c.maxPacket)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Debug("read error", "server", c.serverName, "error", err)
			return
		}
		c.handler.HandlePacket(c, packetType, payload)
	}
}

func (c *Connection) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-c.writeCh:
			c.writeMu.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			c.writer.Write(data)
			// drain any queued frames before flushing
			for len(c.writeCh) > 0 {
				d := <-c.writeCh
				c.writer.Write(d)
			}
			c.writer.Flush()
			c.writeMu.Unlock()
		}
	}
}
