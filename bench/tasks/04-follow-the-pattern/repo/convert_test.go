package fixture

import "testing"

func TestCelsiusToFahrenheit(t *testing.T) {
	if got := CelsiusToFahrenheit(100); got != 212 {
		t.Errorf("got %v, want 212", got)
	}
}

func TestFahrenheitToCelsius(t *testing.T) {
	if got := FahrenheitToCelsius(32); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestCelsiusToKelvin(t *testing.T) {
	if got := CelsiusToKelvin(0); got != 273.15 {
		t.Errorf("got %v, want 273.15", got)
	}
	if got := CelsiusToKelvin(-273.15); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}
