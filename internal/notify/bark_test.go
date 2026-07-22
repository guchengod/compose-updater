package notify

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.day.app":      "https://api.day.app/push",
		"https://api.day.app/":     "https://api.day.app/push",
		"https://api.day.app/push": "https://api.day.app/push",
	}
	for input, expected := range cases {
		actual, err := normalizeEndpoint(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("%s: got %s, want %s", input, actual, expected)
		}
	}
}
