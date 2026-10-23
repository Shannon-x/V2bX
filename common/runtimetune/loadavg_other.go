//go:build !linux

package runtimetune

func loadAverage1() float64 { return 0 }
