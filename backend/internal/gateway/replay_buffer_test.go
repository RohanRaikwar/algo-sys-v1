package gateway

import "testing"

func TestReplayBuffer_Range(t *testing.T) {
	rb := NewReplayBuffer(100)

	for i := int64(1); i <= 10; i++ {
		rb.Push(i, []byte("msg"))
	}

	got := rb.Range(3, 7)
	if len(got) != 5 {
		t.Fatalf("Range(3,7): expected 5, got %d", len(got))
	}
	for i, e := range got {
		expected := int64(i) + 3
		if e.Seq != expected {
			t.Errorf("entry[%d].Seq = %d, want %d", i, e.Seq, expected)
		}
	}
}

func TestReplayBuffer_Wraparound(t *testing.T) {
	rb := NewReplayBuffer(5) // tiny buffer

	// Push 8 entries — first 3 should be evicted
	for i := int64(1); i <= 8; i++ {
		rb.Push(i, []byte("msg"))
	}

	if rb.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", rb.Len())
	}

	// Should only contain seqs 4-8
	got := rb.Range(1, 10)
	if len(got) != 5 {
		t.Fatalf("Range(1,10): expected 5, got %d", len(got))
	}
	if got[0].Seq != 4 {
		t.Errorf("oldest entry seq = %d, want 4", got[0].Seq)
	}
	if got[4].Seq != 8 {
		t.Errorf("newest entry seq = %d, want 8", got[4].Seq)
	}
}

func TestReplayBuffer_Empty(t *testing.T) {
	rb := NewReplayBuffer(10)
	got := rb.Range(1, 100)
	if len(got) != 0 {
		t.Fatalf("empty buffer Range should return 0, got %d", len(got))
	}
}
