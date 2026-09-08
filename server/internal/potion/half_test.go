package potion

import (
	"math"
	"testing"
)

func TestHalfToFloat32(t *testing.T) {
	tests := []struct {
		name  string
		value uint16
		want  float32
	}{
		{name: "positive zero", value: 0x0000, want: 0},
		{name: "negative zero", value: 0x8000, want: float32(math.Copysign(0, -1))},
		{name: "smallest subnormal", value: 0x0001, want: 0.000000059604644775390625},
		{name: "largest subnormal", value: 0x03ff, want: 0.000060975551605224609375},
		{name: "smallest normal", value: 0x0400, want: 0.00006103515625},
		{name: "one", value: 0x3c00, want: 1},
		{name: "negative two", value: 0xc000, want: -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := halfToFloat32(tt.value)
			if got != tt.want {
				t.Fatalf("halfToFloat32(%#x) = %g, want %g", tt.value, got, tt.want)
			}
		})
	}
	if got := halfToFloat32(0x7c00); !math.IsInf(float64(got), 1) {
		t.Fatalf("positive infinity decoded as %v", got)
	}
	if got := halfToFloat32(0xfc00); !math.IsInf(float64(got), -1) {
		t.Fatalf("negative infinity decoded as %v", got)
	}
	if got := halfToFloat32(0x7e00); !math.IsNaN(float64(got)) {
		t.Fatalf("NaN decoded as %v", got)
	}
}
