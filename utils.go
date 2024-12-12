package main

// Definitions for the CRC16 computation
func CRC16(data []byte) uint16 {
	remainder := uint16(0)

	for _, b := range data {
		remainder = CRC16Recursive(b, remainder)
	}
	return remainder
}

func CRC16Recursive(byteVal byte, remainder uint16) uint16 {
	n := 16
	remainder ^= uint16(byteVal) << (n - 8)

	for j := 1; j < 8; j++ {
		if remainder&0x8000 != 0 {
			remainder = (remainder << 1) ^ 0x1021
		} else {
			remainder <<= 1
		}
		remainder &= 0xFFFF
	}
	return remainder
}
