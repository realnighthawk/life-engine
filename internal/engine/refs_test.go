package engine

import "testing"

func TestIDDistanceFindsAMangledID(t *testing.T) {
	a := "3f2b8c1e-5a7d-4e90-b1c2-9d8e7f6a5b4c"
	if d := idDistance(a, "3f2b8c1e-5a7d-4e90-b1c2-9d8e7f6a5b4d"); d != 1 {
		t.Fatalf("one altered character: distance %d", d)
	}
	if d := idDistance(a, "3f2b8c1e-5a7d-4e90-b1c2-9d8e7f6a5b4"); d != 1 {
		t.Fatalf("one dropped character: distance %d", d)
	}
	if d := idDistance(a, "3f2b8c1e-5a7d-4e9-b1c2-9d8e7f6a5b4c"); d != 1 {
		t.Fatalf("one dropped middle character: distance %d", d)
	}
	if d := idDistance(a, "00000000-0000-0000-0000-000000000000"); d <= 4 {
		t.Fatalf("an unrelated id must not be suggested: distance %d", d)
	}
}
