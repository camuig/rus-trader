package vectordb

import (
	"math"
	"testing"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 2, 3}
	sim := CosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 0.001 {
		t.Errorf("identical vectors: expected 1.0, got %f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim) > 0.001 {
		t.Errorf("orthogonal: expected 0, got %f", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim+1.0) > 0.001 {
		t.Errorf("opposite: expected -1.0, got %f", sim)
	}
}

func TestCosineSimilarity_Empty(t *testing.T) {
	sim := CosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("empty: expected 0, got %f", sim)
	}
}

func TestCosineSimilarity_DifferentLength(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("different length: expected 0, got %f", sim)
	}
}

func TestSerializeDeserialize(t *testing.T) {
	original := []float32{1.5, -2.3, 0.0, 100.123}
	data := SerializeEmbedding(original)
	restored := DeserializeEmbedding(data)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}
	for i := range original {
		if math.Abs(float64(original[i]-restored[i])) > 0.0001 {
			t.Errorf("value mismatch at %d: %f vs %f", i, original[i], restored[i])
		}
	}
}

func TestSerializeEmpty(t *testing.T) {
	data := SerializeEmbedding(nil)
	restored := DeserializeEmbedding(data)
	if len(restored) != 0 {
		t.Errorf("expected empty, got %d elements", len(restored))
	}
}

func TestDeserializeInvalid(t *testing.T) {
	restored := DeserializeEmbedding([]byte{1, 2, 3}) // not multiple of 4
	if restored != nil {
		t.Error("expected nil for invalid data")
	}
}
