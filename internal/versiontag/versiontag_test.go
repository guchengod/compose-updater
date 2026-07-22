package versiontag

import "testing"

func TestLatestNumeric(t *testing.T) {
	latest, ok := Latest("1.9.0", []string{"1.8.9", "1.10.0", "2.0.0", "latest", "2.1.0-alpine"})
	if !ok || latest != "2.0.0" {
		t.Fatalf("unexpected latest: %q %v", latest, ok)
	}
}

func TestLatestPreservesVariantAndPrefix(t *testing.T) {
	latest, ok := Latest("v1.2.3-alpine", []string{"1.9.0-alpine", "v1.4.0", "v1.3.0-alpine"})
	if !ok || latest != "v1.3.0-alpine" {
		t.Fatalf("unexpected latest: %q %v", latest, ok)
	}
}

func TestLatestRejectsNonNumeric(t *testing.T) {
	if _, ok := Latest("latest", []string{"1.0.0"}); ok {
		t.Fatal("latest must not be treated as numeric")
	}
}

func TestCompareZeroPadsParts(t *testing.T) {
	left, _ := Parse("1.2")
	right, _ := Parse("1.2.0")
	if Compare(left, right) != 0 {
		t.Fatal("1.2 and 1.2.0 should compare equal")
	}
}
