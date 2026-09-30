package main

import "testing"

func TestExpandMiddlesProducesUniqueValues(t *testing.T) {
	middles := expandMiddles([]string{"A.", "B.", "C.", "D.", "E.", "F.", "G.", "H.", "I.", "J."}, 30)
	if len(middles) != 30 {
		t.Fatalf("len(middles) = %d; esperava 30", len(middles))
	}
	seen := make(map[string]struct{}, len(middles))
	for _, middle := range middles {
		if _, ok := seen[middle]; ok {
			t.Fatalf("middle duplicado: %q", middle)
		}
		seen[middle] = struct{}{}
	}
	if middles[10] != "K." {
		t.Fatalf("primeiro middle gerado = %q; esperava K.", middles[10])
	}
}
