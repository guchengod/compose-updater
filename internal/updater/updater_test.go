package updater

import "testing"

func TestAllEqual(t *testing.T) {
	if !allEqual([]string{"sha256:a", "sha256:a"}, "sha256:a") {
		t.Fatal("expected equal")
	}
	if allEqual([]string{"sha256:a", "sha256:b"}, "sha256:a") {
		t.Fatal("expected mismatch")
	}
	if allEqual(nil, "sha256:a") {
		t.Fatal("empty must not be equal")
	}
}

func TestImageTag(t *testing.T) {
	cases := map[string]string{
		"nginx":                      "latest",
		"nginx:1.2.3":                "1.2.3",
		"registry:5000/team/app:2.0": "2.0",
		"app@sha256:abc":             "",
	}
	for image, expected := range cases {
		if actual := imageTag(image); actual != expected {
			t.Fatalf("%s: got %s, want %s", image, actual, expected)
		}
	}
}
