package upstream

import (
	"net/http"
	"testing"
)

func TestDefaultConnectivityHTTPClientVerifiesTLSByDefault(t *testing.T) {
	t.Parallel()

	client := defaultConnectivityHTTPClient()
	if client.Timeout == 0 {
		t.Fatal("default connectivity client has no timeout")
	}
	transport, _ := client.Transport.(*http.Transport)
	if transport != nil && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default connectivity client disables TLS certificate verification")
	}
}
