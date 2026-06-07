package dnspod

import "testing"

func TestSubdomainFromName(t *testing.T) {
	tests := []struct {
		zone string
		name string
		want string
	}{
		{zone: "example.com", name: "example.com", want: "@"},
		{zone: "example.com", name: "@", want: "@"},
		{zone: "example.com", name: "home.example.com", want: "home"},
		{zone: "example.com", name: "home", want: "home"},
	}
	for _, test := range tests {
		if got := subdomainFromName(test.zone, test.name); got != test.want {
			t.Fatalf("subdomainFromName(%q, %q) = %q, want %q", test.zone, test.name, got, test.want)
		}
	}
}
