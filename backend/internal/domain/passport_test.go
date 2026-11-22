package domain

import (
	"testing"
	"time"
)

func TestPassportSupersededBy(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	stored := PassportAttached{SHA256: "bbbb", Size: 10, AttachedAt: t0}.Metadata()

	cases := []struct {
		name string
		p    PassportAttached
		want bool
	}{
		{"newer wins", PassportAttached{SHA256: "aaaa", AttachedAt: t0.Add(time.Second)}, false},
		{"older loses", PassportAttached{SHA256: "ffff", AttachedAt: t0.Add(-time.Second)}, true},
		{"redelivery is no-op", PassportAttached{SHA256: "bbbb", AttachedAt: t0}, true},
		{"tie: bigger sha wins", PassportAttached{SHA256: "cccc", AttachedAt: t0}, false},
		{"tie: smaller sha loses", PassportAttached{SHA256: "aaaa", AttachedAt: t0}, true},
	}
	for _, c := range cases {
		if got := c.p.SupersededBy(stored); got != c.want {
			t.Errorf("%s: SupersededBy = %v, want %v", c.name, got, c.want)
		}
	}
	if (PassportAttached{SHA256: "aaaa", AttachedAt: t0}).SupersededBy(map[string]string{}) {
		t.Error("horse without passport must accept any passport")
	}
}

func TestPassportMetadataKeepsStudbookWhenEmpty(t *testing.T) {
	m := PassportAttached{SHA256: "x", AttachedAt: time.Now()}.Metadata()
	if _, ok := m[MetaStudbookNumber]; ok {
		t.Fatal("empty studbook number must not overwrite existing value")
	}
}
