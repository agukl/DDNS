package ipgetter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetOutboundIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "1.2.3.4")
	}))
	defer server.Close()

	getter := &Getter{
		Client:    server.Client(),
		Endpoints: []string{server.URL},
		IPType:    "ipv4",
		Timeout:   time.Second,
	}

	ip, err := getter.GetOutboundIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "1.2.3.4" {
		t.Fatalf("ip = %s, want 1.2.3.4", ip)
	}
}
