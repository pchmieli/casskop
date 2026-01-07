package v2

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePercentageString parses a percentage string into a float64
// Accepts formats:
//   - Percentage with % sign: "20%", "5.5%" (range 0-100%)
//   - Fraction without % sign: "0.2", "0.055" (range 0-1, interpreted as percentage)
//
// Returns the percentage as a float (e.g., 20.0 for "20%" or "0.2")
func ParsePercentageString(s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty percentage string")
	}

	// Remove whitespace
	s = strings.TrimSpace(s)

	// Check if it ends with %
	hasPercent := strings.HasSuffix(s, "%")
	if hasPercent {
		// Remove % and parse
		s = strings.TrimSuffix(s, "%")
		value, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid percentage value '%s%%': %w", s, err)
		}
		// Validate range 0-100%
		if value < 0 || value > 100 {
			return 0, fmt.Errorf("percentage value %.2f%% is out of range (0-100%%)", value)
		}
		return value, nil
	}

	// No % sign - must be a fraction (0-1)
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid fraction value '%s': %w", s, err)
	}

	// Validate range 0-1 for fractions
	if value < 0 || value > 1 {
		return 0, fmt.Errorf("fraction value %.3f is out of range (0-1), use format like '0.2' for 20%% or '20%%'", value)
	}

	// Convert fraction to percentage
	return value * 100, nil
}
