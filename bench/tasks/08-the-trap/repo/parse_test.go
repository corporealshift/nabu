package fixture

import "testing"

func TestParseDurationSeconds(t *testing.T) {
	if got, _ := ParseDuration("45s"); got != 45 {
		t.Errorf("got %d, want 45", got)
	}
}

func TestParseDurationMinutes(t *testing.T) {
	if got, _ := ParseDuration("90m"); got != 5400 {
		t.Errorf("got %d, want 5400", got)
	}
}

func TestParseDurationHours(t *testing.T) {
	if got, _ := ParseDuration("2h"); got != 7200 {
		t.Errorf("got %d, want 7200", got)
	}
}

func TestParseDurationRejectsRubbish(t *testing.T) {
	if _, err := ParseDuration("banana"); err == nil {
		t.Error("banana should not parse")
	}
	if _, err := ParseDuration("12x"); err == nil {
		t.Error("an unknown unit should not parse")
	}
}
