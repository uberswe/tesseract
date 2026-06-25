package inventory

import (
	"testing"

	"github.com/uberswe/tesseract/internal/protocol"
)

var testUUID = protocol.UUID{MSB: 1, LSB: 2}

func insOp(slot, count int, nbt []byte) protocol.Operation {
	return protocol.Operation{Type: protocol.OpInsert, Slot: slot, Count: count, ItemNBT: nbt}
}

func extOp(slot, count int) protocol.Operation {
	return protocol.Operation{Type: protocol.OpExtract, Slot: slot, Count: count}
}

func TestInsertIntoEmptySlot(t *testing.T) {
	s := NewStore()
	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 1, []byte("book_a"))})
	if res[0].Status != protocol.ResultAccepted {
		t.Fatalf("expected ResultAccepted, got %d", res[0].Status)
	}
	if got := string(s.GetSlotNBT(testUUID, 0)); got != "book_a" {
		t.Fatalf("slot 0 identity = %q, want book_a", got)
	}
	total, nonEmpty := s.GetStats(testUUID)
	if total != 1 || nonEmpty != 1 {
		t.Fatalf("stats = (total %d, nonEmpty %d), want (1, 1)", total, nonEmpty)
	}
}

// A different item must never merge into an occupied slot. This is the core
// guard for max-stack-1 items: two different enchanted books routed to the same
// slot previously destroyed one and overstacked the other.
func TestInsertDifferentItemSameSlotRejected(t *testing.T) {
	s := NewStore()
	s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 1, []byte("book_a"))})

	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 1, []byte("book_b"))})
	if res[0].Status != protocol.ResultRejectedMismatch {
		t.Fatalf("expected ResultRejectedMismatch, got %d", res[0].Status)
	}
	// Original item and count must be untouched.
	if got := string(s.GetSlotNBT(testUUID, 0)); got != "book_a" {
		t.Fatalf("slot 0 identity = %q, want book_a (must not be overwritten)", got)
	}
	if total, _ := s.GetStats(testUUID); total != 1 {
		t.Fatalf("total = %d, want 1 (must not have overstacked)", total)
	}
}

// The same item identity is allowed to accumulate (caller is responsible for
// respecting per-item max stack size; the service trusts the Java-side clamp).
func TestInsertSameItemAccumulates(t *testing.T) {
	s := NewStore()
	s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 5, []byte("cobble"))})
	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 3, []byte("cobble"))})
	if res[0].Status != protocol.ResultAccepted {
		t.Fatalf("expected ResultAccepted, got %d", res[0].Status)
	}
	if total, _ := s.GetStats(testUUID); total != 8 {
		t.Fatalf("total = %d, want 8", total)
	}
}

// Different items in different slots coexist (the normal enchanted-book case:
// each unique book occupies its own slot).
func TestInsertDifferentItemsDifferentSlots(t *testing.T) {
	s := NewStore()
	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{
		insOp(0, 1, []byte("book_a")),
		insOp(1, 1, []byte("book_b")),
		insOp(2, 1, []byte("book_c")),
	})
	for i, r := range res {
		if r.Status != protocol.ResultAccepted {
			t.Fatalf("op %d: status %d, want accepted", i, r.Status)
		}
	}
	total, nonEmpty := s.GetStats(testUUID)
	if total != 3 || nonEmpty != 3 {
		t.Fatalf("stats = (total %d, nonEmpty %d), want (3, 3)", total, nonEmpty)
	}
}

func TestExtractClearsIdentityWhenEmpty(t *testing.T) {
	s := NewStore()
	s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 1, []byte("book_a"))})

	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{extOp(0, 1)})
	if res[0].Status != protocol.ResultAccepted {
		t.Fatalf("expected ResultAccepted, got %d", res[0].Status)
	}
	if nbt := s.GetSlotNBT(testUUID, 0); nbt != nil {
		t.Fatalf("slot identity = %q, want nil after full extract", nbt)
	}
	if total, _ := s.GetStats(testUUID); total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	// After the slot is emptied, a different item may now occupy it.
	res2, _ := s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, 1, []byte("book_b"))})
	if res2[0].Status != protocol.ResultAccepted {
		t.Fatalf("reusing emptied slot: status %d, want accepted", res2[0].Status)
	}
}

func TestInsertRejectedWhenFull(t *testing.T) {
	s := NewStore()
	s.ProcessBatch(testUUID, []protocol.Operation{insOp(0, MaxTotalItems, []byte("cobble"))})
	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{insOp(1, 1, []byte("dirt"))})
	if res[0].Status != protocol.ResultRejectedFull {
		t.Fatalf("expected ResultRejectedFull, got %d", res[0].Status)
	}
}

func TestExtractFromEmptySlotRejected(t *testing.T) {
	s := NewStore()
	res, _ := s.ProcessBatch(testUUID, []protocol.Operation{extOp(5, 1)})
	if res[0].Status != protocol.ResultRejectedEmpty {
		t.Fatalf("expected ResultRejectedEmpty, got %d", res[0].Status)
	}
}
