package models_test

import (
	"testing"
	"time"

	"github.com/harshadptl/gcore/internal/models"
)

func TestLiveStreamFlips(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t1, t2, t3 := t0.Add(time.Minute), t0.Add(2*time.Minute), t0.Add(3*time.Minute)
	var s models.LiveStream

	s.Apply(true, false, t0)
	if !s.ArmedAt.Equal(t0) || !s.StartedAt.IsZero() {
		t.Fatalf("arm: %+v", s)
	}
	s.Apply(true, true, t1)
	if !s.StartedAt.Equal(t1) || !s.ArmedAt.Equal(t0) {
		t.Fatalf("go live: %+v", s)
	}
	s.Apply(true, true, t2)
	if !s.StartedAt.Equal(t1) || !s.SyncedAt.Equal(t2) {
		t.Fatalf("repeat live must not restamp StartedAt: %+v", s)
	}
	s.Apply(true, false, t3)
	if !s.EndedAt.Equal(t3) {
		t.Fatalf("end: %+v", s)
	}
}

func TestLiveStreamAbandoned(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := models.LiveStream{Active: true, ArmedAt: t0}
	if s.Abandoned(t0.Add(9 * time.Minute)) {
		t.Fatal("abandoned too early")
	}
	if !s.Abandoned(t0.Add(11 * time.Minute)) {
		t.Fatal("should be abandoned")
	}
	s.Live = true
	if s.Abandoned(t0.Add(time.Hour)) {
		t.Fatal("live stream is never abandoned")
	}
	// Went live then dropped at t0+8m: the clock restarts from EndedAt.
	s = models.LiveStream{Active: true, ArmedAt: t0, EndedAt: t0.Add(8 * time.Minute)}
	if s.Abandoned(t0.Add(11 * time.Minute)) {
		t.Fatal("clock must restart from EndedAt")
	}
	if !s.Abandoned(t0.Add(19 * time.Minute)) {
		t.Fatal("should be abandoned 10m after EndedAt")
	}
}
