package timeutil

import "time"

// SameUpdatedAtSecond compares timestamps in UTC with second precision.
func SameUpdatedAtSecond(dbUpdatedAt, selectedUpdatedAt time.Time) bool {
	return dbUpdatedAt.UTC().Truncate(time.Second).Equal(selectedUpdatedAt.UTC().Truncate(time.Second))
}
