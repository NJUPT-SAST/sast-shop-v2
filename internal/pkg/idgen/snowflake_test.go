package idgen

import (
	"strings"
	"testing"
)

func TestNewSnowflakeRejectsInvalidWorkerID(t *testing.T) {
	if _, err := NewSnowflake(-1); err == nil {
		t.Fatal("expected negative worker id to fail")
	}
	if _, err := NewSnowflake(MaxWorkerID + 1); err == nil {
		t.Fatal("expected worker id above max to fail")
	}
}

func TestSnowflakeNextIDIsUniqueAndIncreasing(t *testing.T) {
	sf, err := NewSnowflake(1)
	if err != nil {
		t.Fatal(err)
	}

	const total = 5000
	seen := make(map[int64]struct{}, total)
	var previous int64

	for i := 0; i < total; i++ {
		id, err := sf.NextID()
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && id <= previous {
			t.Fatalf("id is not increasing: previous=%d current=%d", previous, id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id: %d", id)
		}
		seen[id] = struct{}{}
		previous = id
	}
}

func TestOrderNoGeneratorUsesPrefix(t *testing.T) {
	g, err := NewOrderNoGenerator("ET", 1)
	if err != nil {
		t.Fatal(err)
	}

	no, err := g.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(no, "ET") {
		t.Fatalf("expected ET prefix, got %q", no)
	}
}
