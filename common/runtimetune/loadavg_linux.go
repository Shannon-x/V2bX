package runtimetune

import (
	"os"
	"strconv"
	"strings"
)

// loadAverage1 returns the 1-minute load average from /proc/loadavg.
func loadAverage1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}
