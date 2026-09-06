package replay

import "testing"

func TestDeriveSeeds_Deterministic(t *testing.T) {
	a := DeriveSeeds(42)
	b := DeriveSeeds(42)
	if a != b {
		t.Fatalf("DeriveSeeds(42) called twice produced different results: %+v vs %+v", a, b)
	}
}

func TestDeriveSeeds_PairwiseDistinct(t *testing.T) {
	for _, root := range []int64{0, 1, 42, -7, 1_000_000} {
		s := DeriveSeeds(root)
		values := map[string]int64{
			"Traffic": s.Traffic, "Topology": s.Topology, "Failure": s.Failure, "Policy": s.Policy,
		}
		seen := make(map[int64]string, len(values))
		for name, v := range values {
			if other, ok := seen[v]; ok {
				t.Errorf("root %d: %s and %s derived to the same sub-seed %d", root, name, other, v)
			}
			seen[v] = name
		}
	}
}

func TestDeriveSeeds_DifferentRootsDifferentTrees(t *testing.T) {
	a := DeriveSeeds(1)
	b := DeriveSeeds(2)
	if a == b {
		t.Fatal("DeriveSeeds(1) and DeriveSeeds(2) produced identical SeedTrees")
	}
}

func TestDeriveSeeds_AlwaysNonNegative(t *testing.T) {
	for _, root := range []int64{0, 1, -1, -999999, 1 << 62} {
		s := DeriveSeeds(root)
		for name, v := range map[string]int64{"Traffic": s.Traffic, "Topology": s.Topology, "Failure": s.Failure, "Policy": s.Policy} {
			if v < 0 {
				t.Errorf("root %d: derived sub-seed %s = %d, want non-negative", root, name, v)
			}
		}
	}
}

func TestDeriveSeeds_PreservesGlobal(t *testing.T) {
	s := DeriveSeeds(12345)
	if s.Global != 12345 {
		t.Errorf("Global = %d, want 12345", s.Global)
	}
}
