package fixture

// Conversions are written as a named function per pair, each with a one-line
// comment naming the relationship rather than restating the arithmetic.

// CelsiusToFahrenheit converts by the ratio of the degree sizes, offset by the
// difference in their zero points.
func CelsiusToFahrenheit(c float64) float64 {
	return c*9/5 + 32
}

// FahrenheitToCelsius is the inverse of CelsiusToFahrenheit.
func FahrenheitToCelsius(f float64) float64 {
	return (f - 32) * 5 / 9
}
