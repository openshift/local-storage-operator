package common

import (
	"testing"
)

func TestRoundDownCapacityPretty(t *testing.T) {
	var capTests = []struct {
		n        int64 // input
		expected int64 // expected result
	}{
		{100 * KiB, 100 * KiB},
		{10 * MiB, 10 * MiB},
		{100 * MiB, 100 * MiB},
		{10 * GiB, 10 * GiB},
		{10 * TiB, 10 * TiB},
		{9*GiB + 999*MiB, 9*GiB + 999*MiB},
		{10*GiB + 5, 10 * GiB},
		{10*MiB + 5, 10 * MiB},
		{10000*MiB - 1, 9999 * MiB},
		{13*GiB - 1, 12 * GiB},
		{63*MiB - 10, 62 * MiB},
		{12345, 12345},
		{10000*GiB - 1, 9999 * GiB},
		{3*TiB + 2*GiB + 1*MiB, 3*TiB + 2*GiB},
	}
	for _, tt := range capTests {
		actual := RoundDownCapacityPretty(tt.n)
		if actual != tt.expected {
			t.Errorf("roundDownCapacityPretty(%d): expected %d, actual %d", tt.n, tt.expected, actual)
		}
	}
}
