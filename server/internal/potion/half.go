package potion

import "math"

// halfToFloat32 decodes an IEEE-754 binary16 value without a floating-point
// dependency. Safetensors stores F16 values as little-endian uint16 words.
func halfToFloat32(value uint16) float32 {
	sign := uint32(value&0x8000) << 16
	exponent := int((value >> 10) & 0x1f)
	fraction := uint32(value & 0x03ff)

	switch exponent {
	case 0:
		if fraction == 0 {
			return math.Float32frombits(sign)
		}
		// Normalize a subnormal half before converting its exponent.
		for fraction&0x0400 == 0 {
			fraction <<= 1
			exponent--
		}
		fraction &= 0x03ff
		exponent++
	case 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | (fraction << 13))
	}

	exponent += 112 // (127 - 15), converting half to float32 bias.
	return math.Float32frombits(sign | (uint32(exponent) << 23) | (fraction << 13))
}
