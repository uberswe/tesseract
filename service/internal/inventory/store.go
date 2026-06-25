package inventory

import (
	"bytes"
	"encoding/binary"
	"sync"
	"time"

	"github.com/uberswe/tesseract/internal/protocol"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const NumSlots = 160
const MaxTotalItems = 10240

type SlotState struct {
	ItemNBT []byte // opaque item identity blob from client (inner compound tags, no Slot/end)
	Count   int
}

type InventoryState struct {
	mu        sync.Mutex
	Slots     [NumSlots]SlotState
	Total     int
	Timestamp int64
	UpdatedAt time.Time
	Dirty     bool
}

// Entry is used for persistence and legacy compatibility.
type Entry struct {
	Data       []byte
	Timestamp  int64
	UpdatedAt  time.Time
	Dirty      bool
	LastServer string
}

type Store struct {
	mu      sync.RWMutex
	entries map[protocol.UUID]*InventoryState
}

func NewStore() *Store {
	return &Store{
		entries: make(map[protocol.UUID]*InventoryState),
	}
}

func (s *Store) getOrCreate(uuid protocol.UUID) *InventoryState {
	s.mu.RLock()
	state, ok := s.entries[uuid]
	s.mu.RUnlock()
	if ok {
		return state
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok = s.entries[uuid]
	if ok {
		return state
	}
	state = &InventoryState{}
	s.entries[uuid] = state
	return state
}

func (s *Store) ProcessBatch(uuid protocol.UUID, ops []protocol.Operation) ([]protocol.OpResult, int64) {
	state := s.getOrCreate(uuid)
	state.mu.Lock()
	defer state.mu.Unlock()

	results := make([]protocol.OpResult, len(ops))

	for i, op := range ops {
		switch op.Type {
		case protocol.OpInsert:
			results[i] = state.processInsert(op)
		case protocol.OpExtract:
			results[i] = state.processExtract(op)
		default:
			results[i] = protocol.OpResult{Status: protocol.ResultRejectedMismatch}
		}
	}

	state.Timestamp = time.Now().UnixMilli()
	state.UpdatedAt = time.Now()
	state.Dirty = true

	return results, state.Timestamp
}

func (state *InventoryState) processInsert(op protocol.Operation) protocol.OpResult {
	if op.Slot < 0 || op.Slot >= NumSlots {
		return protocol.OpResult{Status: protocol.ResultRejectedMismatch}
	}

	slot := &state.Slots[op.Slot]

	// Reject merging a different item into an occupied slot. Item identity
	// (type + components) is an opaque, count-stripped NBT blob from the Java
	// side. Without this guard, inserting item B into a slot already holding
	// item A would overwrite A's identity while ADDING to the shared count —
	// destroying A and overstacking B (e.g. two different enchanted books).
	if slot.Count > 0 && len(op.ItemNBT) > 0 && !itemTypesMatch(slot.ItemNBT, op.ItemNBT) {
		return protocol.OpResult{Status: protocol.ResultRejectedMismatch}
	}

	available := MaxTotalItems - state.Total
	if available <= 0 {
		return protocol.OpResult{Status: protocol.ResultRejectedFull}
	}

	accepted := op.Count
	if accepted > available {
		accepted = available
	}

	// Store the raw item NBT (Java side validates type matching via super.insertItem)
	if len(op.ItemNBT) > 0 {
		nbt := make([]byte, len(op.ItemNBT))
		copy(nbt, op.ItemNBT)
		slot.ItemNBT = nbt
	}
	slot.Count += accepted
	state.Total += accepted

	if accepted < op.Count {
		return protocol.OpResult{Status: protocol.ResultRejectedFull}
	}
	return protocol.OpResult{Status: protocol.ResultAccepted}
}

func (state *InventoryState) processExtract(op protocol.Operation) protocol.OpResult {
	if op.Slot < 0 || op.Slot >= NumSlots {
		return protocol.OpResult{Status: protocol.ResultRejectedEmpty}
	}

	slot := &state.Slots[op.Slot]

	if slot.Count <= 0 {
		return protocol.OpResult{Status: protocol.ResultRejectedEmpty}
	}

	extracted := op.Count
	if extracted > slot.Count {
		extracted = slot.Count
	}

	slot.Count -= extracted
	state.Total -= extracted

	if slot.Count == 0 {
		slot.ItemNBT = nil
	}

	if extracted <= 0 {
		return protocol.OpResult{Status: protocol.ResultRejectedInsufficient}
	}
	return protocol.OpResult{Status: protocol.ResultAccepted}
}

func itemTypesMatch(a, b []byte) bool {
	return bytes.Equal(a, b)
}

// stripCountTag removes the "count" TAG_Int from inner compound NBT bytes.
// The count is tracked separately in SlotState.Count.
func stripCountTag(data []byte) []byte {
	var result bytes.Buffer
	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}
		tagType := data[offset]
		if tagType == 0 { // TAG_End
			break
		}
		offset++
		if offset+2 > len(data) {
			break
		}
		nameLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+nameLen > len(data) {
			break
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		// Calculate payload size
		payloadStart := offset
		switch tagType {
		case 1: // TAG_Byte
			offset++
		case 2: // TAG_Short
			offset += 2
		case 3: // TAG_Int
			offset += 4
		case 4: // TAG_Long
			offset += 8
		case 5: // TAG_Float
			offset += 4
		case 6: // TAG_Double
			offset += 8
		case 7: // TAG_Byte_Array
			if offset+4 > len(data) {
				return data
			}
			arrLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4 + arrLen
		case 8: // TAG_String
			if offset+2 > len(data) {
				return data
			}
			strLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2 + strLen
		default:
			// Complex tag — can't easily skip, return original
			return data
		}

		if name == "count" && tagType == 3 {
			// Skip this tag
			continue
		}

		// Write tag type + name + payload
		result.WriteByte(tagType)
		binary.Write(&result, binary.BigEndian, uint16(nameLen))
		result.WriteString(name)
		result.Write(data[payloadStart:offset])
	}

	if result.Len() == 0 {
		return data
	}
	return result.Bytes()
}

// GetStats returns the total item count and non-empty slot count for a UUID.
func (s *Store) GetStats(uuid protocol.UUID) (totalItems int, nonEmptySlots int) {
	s.mu.RLock()
	state, ok := s.entries[uuid]
	s.mu.RUnlock()
	if !ok {
		return 0, 0
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for i := range state.Slots {
		if state.Slots[i].Count > 0 {
			nonEmptySlots++
			totalItems += state.Slots[i].Count
		}
	}
	return
}

// Snapshot returns a compressed NBT blob of the current inventory state.
// Must be called while holding the state's mutex.
func (state *InventoryState) Snapshot() ([]byte, error) {
	return SerializeInventoryNBT(state.Slots, state.Timestamp)
}

// Get returns the inventory state for a UUID as a compressed NBT blob (for INV_REQUEST).
func (s *Store) Get(uuid protocol.UUID) (*Entry, bool) {
	s.mu.RLock()
	state, ok := s.entries[uuid]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	data, err := state.Snapshot()
	if err != nil {
		return nil, false
	}
	return &Entry{
		Data:      data,
		Timestamp: state.Timestamp,
		UpdatedAt: state.UpdatedAt,
	}, true
}

// Set initializes the inventory state from a compressed NBT blob (DB load).
func (s *Store) Set(uuid protocol.UUID, data []byte, timestamp int64) {
	slots, ts, err := ParseInventoryNBT(data)
	if err != nil {
		slots = [NumSlots]SlotState{}
		ts = timestamp
	}
	if ts == 0 {
		ts = timestamp
	}

	total := 0
	for i := range slots {
		total += slots[i].Count
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[uuid] = &InventoryState{
		Slots:     slots,
		Total:     total,
		Timestamp: ts,
		UpdatedAt: time.Now(),
		Dirty:     false,
	}
}

// GetSlotNBT returns the ItemNBT for a specific slot (used for EXTRACT results).
func (s *Store) GetSlotNBT(uuid protocol.UUID, slot int) []byte {
	s.mu.RLock()
	state, ok := s.entries[uuid]
	s.mu.RUnlock()
	if !ok || slot < 0 || slot >= NumSlots {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	nbt := state.Slots[slot].ItemNBT
	if len(nbt) == 0 {
		return nil
	}
	cpy := make([]byte, len(nbt))
	copy(cpy, nbt)
	return cpy
}

// SnapshotLocked creates a snapshot while holding the store's read lock.
// The caller must hold the inventory state's mutex.
func (s *Store) SnapshotFor(uuid protocol.UUID) ([]byte, int64, error) {
	s.mu.RLock()
	state, ok := s.entries[uuid]
	s.mu.RUnlock()
	if !ok {
		return nil, 0, nil
	}
	// Caller should already hold state.mu if needed for consistency
	data, err := state.Snapshot()
	return data, state.Timestamp, err
}

// Push handles v1 protocol: full inventory state push with optimistic locking.
// Kept for backward compatibility during migration.
func (s *Store) Push(uuid protocol.UUID, expectedTs int64, data []byte, serverName string) (accepted bool, ts int64, currentData []byte) {
	state := s.getOrCreate(uuid)
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.Timestamp != 0 && state.Timestamp != expectedTs {
		snapshot, err := state.Snapshot()
		if err != nil {
			return false, state.Timestamp, nil
		}
		return false, state.Timestamp, snapshot
	}

	slots, _, err := ParseInventoryNBT(data)
	if err != nil {
		return false, state.Timestamp, nil
	}

	total := 0
	for i := range slots {
		total += slots[i].Count
	}

	state.Slots = slots
	state.Total = total
	state.Timestamp = time.Now().UnixMilli()
	state.UpdatedAt = time.Now()
	state.Dirty = true

	snapshot, err := state.Snapshot()
	if err != nil {
		return true, state.Timestamp, data
	}
	return true, state.Timestamp, snapshot
}

// DrainDirty returns all dirty entries as compressed NBT blobs for persistence.
func (s *Store) DrainDirty() map[protocol.UUID]*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dirty := make(map[protocol.UUID]*Entry)
	for uuid, state := range s.entries {
		state.mu.Lock()
		if state.Dirty {
			data, err := state.Snapshot()
			if err == nil {
				dirty[uuid] = &Entry{
					Data:      data,
					Timestamp: state.Timestamp,
					UpdatedAt: state.UpdatedAt,
				}
			}
			state.Dirty = false
		}
		state.mu.Unlock()
	}
	return dirty
}

// FlushAll returns all entries as compressed NBT blobs.
func (s *Store) FlushAll() map[protocol.UUID]*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make(map[protocol.UUID]*Entry)
	for uuid, state := range s.entries {
		state.mu.Lock()
		data, err := state.Snapshot()
		if err == nil {
			all[uuid] = &Entry{
				Data:      data,
				Timestamp: state.Timestamp,
				UpdatedAt: state.UpdatedAt,
			}
		}
		state.mu.Unlock()
	}
	return all
}
