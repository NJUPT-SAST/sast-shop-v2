package service

import (
	"testing"
	"time"
)

func TestSameUpdatedAtVersion(t *testing.T) {
	t.Parallel()

	dbUpdatedAt := time.Date(2026, 8, 3, 9, 10, 11, 123456000, time.UTC)
	clientUpdatedAt := time.Date(2026, 8, 3, 17, 10, 11, 123456000, time.FixedZone("UTC+8", 8*60*60))

	if !sameUpdatedAtVersion(dbUpdatedAt, clientUpdatedAt) {
		t.Fatal("the same version in different time zones must match")
	}

	if sameUpdatedAtVersion(dbUpdatedAt, clientUpdatedAt.Add(time.Second)) {
		t.Fatal("timestamps in different UTC seconds must not match")
	}
}

func TestSameUpdatedAtVersionToleratesSubMillisecondDifference(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 8, 3, 9, 10, 11, 123456000, time.UTC)
	if !sameUpdatedAtVersion(current, current.Add(time.Microsecond)) {
		t.Fatal("sub-millisecond differences are the same version: JS Date round-trips preserve only milliseconds")
	}
}

func TestSameUpdatedAtVersionConflictsAcrossMillisecondBoundary(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 8, 3, 9, 10, 11, 123999000, time.UTC)
	if sameUpdatedAtVersion(current, current.Add(time.Microsecond)) {
		t.Fatal("a 1µs difference crossing a millisecond boundary is a different version and must conflict")
	}
}
