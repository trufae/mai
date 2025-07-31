package vectordb

import "math"

func (db *VectorDB) simpleHash(s string) int {
	hash := 0
	for _, c := range s {
		hash = ((hash << 5) - hash) + int(c)
		hash &= 0xFFFFFFFF
	}
	if hash < 0 {
		return -hash
	}
	return hash
}

func normalizeVector(vec []float32) []float32 {
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum == 0 {
		return vec
	}
	norm := float32(math.Sqrt(float64(sum)))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func squaredDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return math.MaxFloat64
	}
	var sum float64
	for i := range a {
		diff := float64(a[i] - b[i])
		sum += diff * diff
	}
	return sum
}
