package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	TypeHello        byte = 0x01
	TypeHelloAck     byte = 0x02
	TypeSubscribe    byte = 0x03
	TypeUnsubscribe  byte = 0x04
	TypeInvPush      byte = 0x05
	TypeInvUpdate    byte = 0x06
	TypeInvRequest   byte = 0x07
	TypeInvResponse  byte = 0x08
	TypePing         byte = 0x09
	TypePong           byte = 0x0A
	TypeInvPushReject byte = 0x0B
	TypeInvPushAck   byte = 0x0C
	TypeBatchOps     byte = 0x0D
	TypeBatchResult  byte = 0x0E

	StatusOK       byte = 0x00
	StatusRejected byte = 0x01

	OpInsert  byte = 0x01
	OpExtract byte = 0x02

	ResultAccepted             byte = 0x00
	ResultRejectedFull         byte = 0x01
	ResultRejectedEmpty        byte = 0x02
	ResultRejectedMismatch     byte = 0x03
	ResultRejectedInsufficient byte = 0x04

	ProtocolV1 byte = 0x01
	ProtocolV2 byte = 0x02
)

func ReadFrame(r io.Reader, maxSize int) (byte, []byte, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, nil, fmt.Errorf("read length: %w", err)
	}
	if length < 1 {
		return 0, nil, fmt.Errorf("packet too small: %d", length)
	}
	if int(length) > maxSize {
		return 0, nil, fmt.Errorf("packet too large: %d > %d", length, maxSize)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, fmt.Errorf("read payload: %w", err)
	}
	return buf[0], buf[1:], nil
}

func WriteFrame(w io.Writer, packetType byte, payload []byte) error {
	length := uint32(1 + len(payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := w.Write([]byte{packetType}); err != nil {
		return fmt.Errorf("write type: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}
	return nil
}

func EncodeUUID(msb, lsb uint64) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], msb)
	binary.BigEndian.PutUint64(buf[8:16], lsb)
	return buf
}

func DecodeUUID(data []byte) (msb, lsb uint64) {
	msb = binary.BigEndian.Uint64(data[0:8])
	lsb = binary.BigEndian.Uint64(data[8:16])
	return
}

type UUID struct {
	MSB uint64
	LSB uint64
}

func (u UUID) String() string {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[0:8], u.MSB)
	binary.BigEndian.PutUint64(b[8:16], u.LSB)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func ParseUUIDFromBytes(data []byte) UUID {
	return UUID{
		MSB: binary.BigEndian.Uint64(data[0:8]),
		LSB: binary.BigEndian.Uint64(data[8:16]),
	}
}

func MakeHello(serverName string) []byte {
	nameBytes := []byte(serverName)
	buf := make([]byte, 2+len(nameBytes))
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(nameBytes)))
	copy(buf[2:], nameBytes)
	return buf
}

func ParseHello(data []byte) (string, error) {
	if len(data) < 2 {
		return "", fmt.Errorf("hello too short")
	}
	nameLen := binary.BigEndian.Uint16(data[0:2])
	if int(nameLen) > len(data)-2 {
		return "", fmt.Errorf("name length exceeds data")
	}
	return string(data[2 : 2+nameLen]), nil
}

func MakeHelloAck(status byte) []byte {
	return []byte{status}
}

func MakeInvPush(uuid UUID, timestamp int64, nbtData []byte) []byte {
	buf := make([]byte, 16+8+len(nbtData))
	binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
	binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
	binary.BigEndian.PutUint64(buf[16:24], uint64(timestamp))
	copy(buf[24:], nbtData)
	return buf
}

func ParseInvPush(data []byte) (UUID, int64, []byte, error) {
	if len(data) < 24 {
		return UUID{}, 0, nil, fmt.Errorf("inv_push too short: %d", len(data))
	}
	uuid := ParseUUIDFromBytes(data[0:16])
	timestamp := int64(binary.BigEndian.Uint64(data[16:24]))
	return uuid, timestamp, data[24:], nil
}

func MakeInvUpdate(uuid UUID, timestamp int64, nbtData []byte) []byte {
	return MakeInvPush(uuid, timestamp, nbtData)
}

func MakeInvRequest(uuid UUID) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
	binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
	return buf
}

func MakeInvResponse(uuid UUID, found bool, timestamp int64, nbtData []byte) []byte {
	if !found {
		buf := make([]byte, 17)
		binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
		binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
		buf[16] = 0
		return buf
	}
	buf := make([]byte, 17+8+len(nbtData))
	binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
	binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
	buf[16] = 1
	binary.BigEndian.PutUint64(buf[17:25], uint64(timestamp))
	copy(buf[25:], nbtData)
	return buf
}

func MakePing(timestamp int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(timestamp))
	return buf
}

func MakeInvPushAck(uuid UUID, timestamp int64) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
	binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
	binary.BigEndian.PutUint64(buf[16:24], uint64(timestamp))
	return buf
}

func MakePong(echoTimestamp int64) []byte {
	return MakePing(echoTimestamp)
}

type Operation struct {
	Type    byte
	Slot    int
	Count   int
	ItemNBT []byte // only for INSERT
}

type OpResult struct {
	Status byte
}

func ParseHelloVersion(data []byte) (string, byte, error) {
	if len(data) < 2 {
		return "", 0, fmt.Errorf("hello too short")
	}
	nameLen := binary.BigEndian.Uint16(data[0:2])
	if int(nameLen) > len(data)-2 {
		return "", 0, fmt.Errorf("name length exceeds data")
	}
	name := string(data[2 : 2+nameLen])
	version := ProtocolV1
	if int(2+nameLen) < len(data) {
		version = data[2+nameLen]
	}
	return name, version, nil
}

func ParseBatchOps(data []byte) (UUID, []Operation, error) {
	if len(data) < 18 {
		return UUID{}, nil, fmt.Errorf("batch_ops too short: %d", len(data))
	}
	uuid := ParseUUIDFromBytes(data[0:16])
	opCount := binary.BigEndian.Uint16(data[16:18])
	offset := 18
	ops := make([]Operation, 0, opCount)
	for i := 0; i < int(opCount); i++ {
		if offset+4 > len(data) {
			return UUID{}, nil, fmt.Errorf("batch_ops truncated at op %d", i)
		}
		opType := data[offset]
		slot := int(data[offset+1])
		count := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		var itemNBT []byte
		if opType == OpInsert {
			if offset+4 > len(data) {
				return UUID{}, nil, fmt.Errorf("batch_ops truncated at insert nbt length op %d", i)
			}
			nbtLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
			if offset+nbtLen > len(data) {
				return UUID{}, nil, fmt.Errorf("batch_ops truncated at insert nbt data op %d", i)
			}
			itemNBT = make([]byte, nbtLen)
			copy(itemNBT, data[offset:offset+nbtLen])
			offset += nbtLen
		}
		ops = append(ops, Operation{
			Type:    opType,
			Slot:    slot,
			Count:   count,
			ItemNBT: itemNBT,
		})
	}
	return uuid, ops, nil
}

func MakeBatchResult(uuid UUID, timestamp int64, results []OpResult, snapshot []byte) []byte {
	size := 16 + 8 + 2 + len(results) + 4 + len(snapshot)
	buf := make([]byte, size)
	binary.BigEndian.PutUint64(buf[0:8], uuid.MSB)
	binary.BigEndian.PutUint64(buf[8:16], uuid.LSB)
	binary.BigEndian.PutUint64(buf[16:24], uint64(timestamp))
	binary.BigEndian.PutUint16(buf[24:26], uint16(len(results)))
	offset := 26
	for _, r := range results {
		buf[offset] = r.Status
		offset++
	}
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(snapshot)))
	offset += 4
	copy(buf[offset:], snapshot)
	return buf
}
