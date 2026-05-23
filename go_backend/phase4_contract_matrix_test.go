package narraforkgo

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPhase4ContractMatrixIsValidJSON(t *testing.T) {
	data, err := os.ReadFile("phase4-contract-matrix.json")
	if err != nil {
		t.Fatalf("read phase4 contract matrix: %v", err)
	}
	var matrix map[string]any
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("phase4-contract-matrix.json must be valid JSON: %v", err)
	}
	if len(matrix) == 0 {
		t.Fatalf("phase4-contract-matrix.json must not be empty")
	}
}
