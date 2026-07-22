package cronexpr

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse("0 4 * * *", loc)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 21, 3, 59, 30, 0, loc)
	next, err := s.Next(start)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 21, 4, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestStepAndAliases(t *testing.T) {
	s, err := Parse("*/15 9-18 * JAN,MAR MON-FRI", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	match := time.Date(2026, time.March, 2, 9, 30, 0, 0, time.UTC)
	if !s.Matches(match) {
		t.Fatalf("expected %v to match", match)
	}
	notMatch := time.Date(2026, time.March, 1, 9, 30, 0, 0, time.UTC)
	if s.Matches(notMatch) {
		t.Fatalf("expected %v not to match", notMatch)
	}
}

func TestDomDowOrSemantics(t *testing.T) {
	s, err := Parse("0 0 1 * MON", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	mondayNotFirst := time.Date(2026, time.June, 8, 0, 0, 0, 0, time.UTC)
	firstNotMonday := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	if !s.Matches(mondayNotFirst) || !s.Matches(firstNotMonday) {
		t.Fatal("day-of-month and day-of-week should use OR semantics when both are restricted")
	}
}
