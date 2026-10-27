package main

import "testing"

func TestCodeFromRedirect(t *testing.T) {
	const state = "abc"
	tests := []struct {
		in, want string
		err      bool
	}{
		{"https://stats.example.com/?state=abc&code=4/xyz&scope=s", "4/xyz", false},
		{"https://stats.example.com/?code=4/xyz&state=other", "", true},
		{"https://stats.example.com/?error=access_denied&state=abc", "", true},
		{"4/bare-code", "4/bare-code", false},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := codeFromRedirect(tt.in, state)
		if (err != nil) != tt.err || got != tt.want {
			t.Errorf("codeFromRedirect(%q) = %q, %v; want %q, error %v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func TestMissingScopes(t *testing.T) {
	granted := "https://www.googleapis.com/auth/googlehealth.sleep.readonly https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly"
	got := missingScopes(granted)
	if len(got) != 3 || got[0] != "googlehealth.health_metrics_and_measurements.readonly" || got[1] != "googlehealth.location.readonly" {
		t.Errorf("missingScopes = %v", got)
	}
}
