package extract

import "runtime"

func defaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8 // external tools are heavy; don't fork dozens at once
	}
	return n
}
