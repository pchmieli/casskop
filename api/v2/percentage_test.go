package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePercentageString_ValidCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
	}{
		// Valid percentage format (with %)
		{"valid percentage - integer", "20%", 20.0},
		{"valid percentage - decimal", "5.5%", 5.5},
		{"valid percentage - zero", "0%", 0.0},
		{"valid percentage - maximum", "100%", 100.0},
		{"valid percentage - with whitespace", "  15%  ", 15.0},
		{"valid percentage - small decimal", "0.05%", 0.05},
		{"very small percentage", "0.001%", 0.001},
		{"percentage with many decimals", "12.3456%", 12.3456},

		// Valid fraction format (without %)
		{"valid fraction - 0.2", "0.2", 20.0},
		{"valid fraction - 0.055", "0.055", 5.5},
		{"valid fraction - zero", "0", 0.0},
		{"valid fraction - one", "1", 100.0},
		{"valid fraction - 0.01", "0.01", 1.0},
		{"valid fraction - with whitespace", "  0.5  ", 50.0},
		{"very small fraction", "0.001", 0.1},
		{"fraction with many decimals", "0.123456", 12.3456},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePercentageString(tt.input)

			assert.NoError(t, err, "Expected no error for input '%s'", tt.input)
			assert.InDelta(t, tt.expected, result, 0.0001, "Expected %.4f, got %.4f for input '%s'", tt.expected, result, tt.input)
		})
	}
}

func TestParsePercentageString_ErrorCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		errorMsg string
	}{
		// Invalid cases - empty/malformed
		{"empty string", "", "empty percentage string"},
		{"only whitespace", "   ", "invalid fraction value"},
		{"invalid characters with %", "abc%", "invalid percentage value"},
		{"invalid characters without %", "xyz", "invalid fraction value"},
		{"just percent sign", "%", "invalid percentage value"},

		// Invalid cases - out of range percentage
		{"percentage above 100", "101%", "out of range (0-100%)"},
		{"percentage negative", "-5%", "out of range (0-100%)"},
		{"percentage way too high", "200%", "out of range (0-100%)"},

		// Invalid cases - out of range fraction
		{"fraction above 1", "1.5", "out of range (0-1)"},
		{"fraction negative", "-0.1", "out of range (0-1)"},
		{"fraction as integer > 1", "20", "out of range (0-1)"},
		{"fraction way too high", "5.0", "out of range (0-1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePercentageString(tt.input)

			assert.Error(t, err, "Expected error for input '%s'", tt.input)
			assert.Contains(t, err.Error(), tt.errorMsg, "Expected error message to contain '%s' for input '%s'", tt.errorMsg, tt.input)
			assert.Zero(t, result, "Expected zero result on error for input '%s'", tt.input)
		})
	}
}
