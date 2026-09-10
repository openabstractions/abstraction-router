package router

// The part of the GPU instrument that is arithmetic rather than platform. It
// lived in gpu_windows.go until 2026-09-08, where the file suffix hid it from
// every platform but one and the tests that call it stopped compiling for two.
func round(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
