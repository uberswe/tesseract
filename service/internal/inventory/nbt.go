package inventory

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	tagEnd       byte = 0
	tagByte      byte = 1
	tagShort     byte = 2
	tagInt       byte = 3
	tagLong      byte = 4
	tagFloat     byte = 5
	tagDouble    byte = 6
	tagByteArray byte = 7
	tagString    byte = 8
	tagList      byte = 9
	tagCompound  byte = 10
	tagIntArray  byte = 11
	tagLongArray byte = 12
)

// SerializeInventoryNBT produces a gzip-compressed Minecraft NBT blob from slot states.
// Format: root CompoundTag { "Items": ListTag(CompoundTag)[54], "Timestamp": Long }
func SerializeInventoryNBT(slots [NumSlots]SlotState, timestamp int64) ([]byte, error) {
	var raw bytes.Buffer
	w := &nbtWriter{w: &raw}

	// Root compound (unnamed for gzip-compressed format — Minecraft expects TAG_Compound with empty name)
	w.writeType(tagCompound)
	w.writeString("") // root name

	// "Items" list of compounds
	w.writeType(tagList)
	w.writeString("Items")
	w.writeByte(tagCompound)
	w.writeInt32(int32(NumSlots))

	for i := 0; i < NumSlots; i++ {
		s := &slots[i]
		if s.Count <= 0 || len(s.ItemNBT) == 0 {
			// Empty slot: compound with only Slot tag
			w.writeType(tagInt)
			w.writeString("Slot")
			w.writeInt32(int32(i))
			w.writeType(tagEnd)
		} else {
			w.writeRawCompoundWithSlotAndCount(i, s.ItemNBT, int32(s.Count))
		}
	}

	// "Timestamp" long
	w.writeType(tagLong)
	w.writeString("Timestamp")
	w.writeInt64(timestamp)

	// End root compound
	w.writeType(tagEnd)

	if w.err != nil {
		return nil, fmt.Errorf("nbt serialize: %w", w.err)
	}

	var compressed bytes.Buffer
	gz, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

// ParseInventoryNBT reads a gzip-compressed Minecraft NBT blob into slot states.
// Returns the slot states and the timestamp from the blob.
func ParseInventoryNBT(data []byte) ([NumSlots]SlotState, int64, error) {
	var slots [NumSlots]SlotState

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return slots, 0, fmt.Errorf("gzip open: %w", err)
	}
	defer gz.Close()

	raw, err := io.ReadAll(gz)
	if err != nil {
		return slots, 0, fmt.Errorf("gzip read: %w", err)
	}

	r := &nbtReader{data: raw}

	// Root compound
	rootType := r.readByte()
	if rootType != tagCompound {
		return slots, 0, fmt.Errorf("expected root compound, got %d", rootType)
	}
	r.readString() // root name (ignored)

	var timestamp int64

	for {
		t := r.readByte()
		if t == tagEnd || r.err != nil {
			break
		}
		name := r.readString()

		switch {
		case name == "Items" && t == tagList:
			listType := r.readByte()
			listLen := r.readInt32()
			if listType != tagCompound {
				return slots, 0, fmt.Errorf("Items list type %d, expected compound", listType)
			}
			for i := 0; i < int(listLen); i++ {
				slotIndex, nbtBytes, count := r.readItemCompound()
				if r.err != nil {
					return slots, 0, fmt.Errorf("reading item %d: %w", i, r.err)
				}
				if slotIndex >= 0 && slotIndex < NumSlots && count > 0 {
					slots[slotIndex] = SlotState{
						ItemNBT: nbtBytes,
						Count:   count,
					}
				}
			}
		case name == "Timestamp" && t == tagLong:
			timestamp = r.readInt64()
		default:
			r.skipTag(t)
		}
	}

	if r.err != nil {
		return slots, 0, fmt.Errorf("nbt parse: %w", r.err)
	}

	return slots, timestamp, nil
}

type nbtWriter struct {
	w   io.Writer
	err error
}

func (w *nbtWriter) writeType(t byte) {
	w.writeByte(t)
}

func (w *nbtWriter) writeByte(b byte) {
	if w.err != nil {
		return
	}
	_, w.err = w.w.Write([]byte{b})
}

func (w *nbtWriter) writeString(s string) {
	if w.err != nil {
		return
	}
	b := []byte(s)
	w.err = binary.Write(w.w, binary.BigEndian, uint16(len(b)))
	if w.err != nil {
		return
	}
	_, w.err = w.w.Write(b)
}

func (w *nbtWriter) writeInt32(v int32) {
	if w.err != nil {
		return
	}
	w.err = binary.Write(w.w, binary.BigEndian, v)
}

func (w *nbtWriter) writeInt64(v int64) {
	if w.err != nil {
		return
	}
	w.err = binary.Write(w.w, binary.BigEndian, v)
}

// writeRawCompoundWithSlotAndCount writes a compound tag using the stored item NBT bytes,
// injecting a Slot byte tag and a count int tag with the current value.
func (w *nbtWriter) writeRawCompoundWithSlotAndCount(slot int, itemNBT []byte, count int32) {
	if w.err != nil {
		return
	}
	// Slot tag
	w.writeType(tagInt)
	w.writeString("Slot")
	w.writeInt32(int32(slot))
	// Write the raw item compound contents first (may contain old count)
	_, w.err = w.w.Write(itemNBT)
	if w.err != nil {
		return
	}
	// Write count AFTER raw NBT so it overwrites any embedded count
	w.writeType(tagInt)
	w.writeString("count")
	w.writeInt32(count)
	// End compound
	w.writeType(tagEnd)
}

type nbtReader struct {
	data   []byte
	offset int
	err    error
}

func (r *nbtReader) readByte() byte {
	if r.err != nil || r.offset >= len(r.data) {
		r.err = fmt.Errorf("unexpected end of data at offset %d", r.offset)
		return 0
	}
	b := r.data[r.offset]
	r.offset++
	return b
}

func (r *nbtReader) readInt16() int16 {
	if r.err != nil || r.offset+2 > len(r.data) {
		r.err = fmt.Errorf("unexpected end of data at offset %d", r.offset)
		return 0
	}
	v := int16(binary.BigEndian.Uint16(r.data[r.offset : r.offset+2]))
	r.offset += 2
	return v
}

func (r *nbtReader) readInt32() int32 {
	if r.err != nil || r.offset+4 > len(r.data) {
		r.err = fmt.Errorf("unexpected end of data at offset %d", r.offset)
		return 0
	}
	v := int32(binary.BigEndian.Uint32(r.data[r.offset : r.offset+4]))
	r.offset += 4
	return v
}

func (r *nbtReader) readInt64() int64 {
	if r.err != nil || r.offset+8 > len(r.data) {
		r.err = fmt.Errorf("unexpected end of data at offset %d", r.offset)
		return 0
	}
	v := int64(binary.BigEndian.Uint64(r.data[r.offset : r.offset+8]))
	r.offset += 8
	return v
}

func (r *nbtReader) readString() string {
	length := int(r.readInt16())
	if r.err != nil || r.offset+length > len(r.data) {
		r.err = fmt.Errorf("string too long at offset %d", r.offset)
		return ""
	}
	s := string(r.data[r.offset : r.offset+length])
	r.offset += length
	return s
}

func (r *nbtReader) readFloat32() {
	r.offset += 4
}

func (r *nbtReader) readFloat64() {
	r.offset += 8
}

// readItemCompound reads a compound tag representing an item stack.
// Returns the slot index, the raw inner tag bytes (excluding Slot and count), and the count.
// The raw bytes are suitable for storage as SlotState.ItemNBT.
func (r *nbtReader) readItemCompound() (slotIndex int, nbtBytes []byte, count int) {
	slotIndex = -1
	count = 0
	var innerTags bytes.Buffer

	for {
		t := r.readByte()
		if t == tagEnd || r.err != nil {
			break
		}
		name := r.readString()

		if name == "Slot" && t == tagByte {
			slotIndex = int(int8(r.readByte()))
			continue
		}

		if name == "Slot" && t == tagInt {
			slotIndex = int(r.readInt32())
			continue
		}

		if name == "count" && t == tagInt {
			count = int(r.readInt32())
			// Don't include count in stored ItemNBT — it's tracked separately
			// and written with the current value during serialization
			continue
		}

		// Capture the tag into innerTags
		startOffset := r.offset
		tagHeaderStart := r.offset - 2 - len(name) - 1 // type + name_len + name
		r.skipTagPayload(t)
		if r.err != nil {
			return
		}
		// Write the full tag: type + name + payload
		innerTags.WriteByte(t)
		binary.Write(&innerTags, binary.BigEndian, uint16(len(name)))
		innerTags.WriteString(name)
		innerTags.Write(r.data[startOffset:r.offset])
		_ = tagHeaderStart
	}

	if innerTags.Len() > 0 {
		nbtBytes = innerTags.Bytes()
	}
	return
}

func (r *nbtReader) skipTag(t byte) {
	r.skipTagPayload(t)
}

func (r *nbtReader) skipTagPayload(t byte) {
	if r.err != nil {
		return
	}
	switch t {
	case tagByte:
		r.offset++
	case tagShort:
		r.offset += 2
	case tagInt:
		r.offset += 4
	case tagLong:
		r.offset += 8
	case tagFloat:
		r.offset += 4
	case tagDouble:
		r.offset += 8
	case tagByteArray:
		length := r.readInt32()
		r.offset += int(length)
	case tagString:
		r.readString()
	case tagList:
		listType := r.readByte()
		listLen := r.readInt32()
		for i := 0; i < int(listLen); i++ {
			r.skipTagPayload(listType)
		}
	case tagCompound:
		for {
			ct := r.readByte()
			if ct == tagEnd || r.err != nil {
				break
			}
			r.readString() // name
			r.skipTagPayload(ct)
		}
	case tagIntArray:
		length := r.readInt32()
		r.offset += int(length) * 4
	case tagLongArray:
		length := r.readInt32()
		r.offset += int(length) * 8
	default:
		r.err = fmt.Errorf("unknown tag type %d at offset %d", t, r.offset)
	}
}
