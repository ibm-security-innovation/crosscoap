package crosscoap

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dustin/go-coap"
)

func TestProxyWithConfirmableRequest(t *testing.T) {
	const backendResponse = "<body>This is the response text</body>"
	const backendStatus = 404
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("backend got method %q, want %q", r.Method, "POST")
		}
		if r.URL.String() != "/base/dir/some/path" {
			t.Errorf("backend got URL %q, want %q", r.URL, "/base/dir/some/path")
		}
		if len(r.TransferEncoding) > 0 {
			t.Errorf("backend got unexpected Transfer-Encoding: %v", r.TransferEncoding)
		}
		if r.UserAgent() != "crosscoap/1.0" {
			t.Errorf("backend got unexpected User-Agent: %v", r.UserAgent())
		}
		//if r.Header.Get("X-Forwarded-For") == "" {
		//	t.Errorf("didn't get X-Forwarded-For header")
		//}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(backendStatus)
		w.Write([]byte(backendResponse))
	}))
	defer backend.Close()

	udpListener, crosscoapAddr := createLocalUDPListener(t)
	defer udpListener.Close()
	proxy := Proxy{Listener: udpListener, BackendURL: backend.URL + "/base/dir"}
	go proxy.Serve()

	req := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.POST,
		MessageID: 12345,
		Payload:   []byte(`{"key":"Content of CoAP packet payload"}`),
	}
	req.SetPathString("/some/path")
	req.SetOption(coap.ContentFormat, coap.AppJSON)

	c, err := coap.Dial("udp", crosscoapAddr)
	if err != nil {
		t.Fatalf("Error dialing: %v", err)
	}
	rv, err := c.Send(req)
	if err != nil {
		t.Fatalf("Error sending request: %v", err)
	}
	if rv == nil {
		t.Fatalf("Didn't receive CoAP response")
	}

	if string(rv.Payload) != backendResponse {
		t.Errorf("got body %q; expected %q", string(rv.Payload), backendResponse)
	}
	if rv.Code != coap.NotFound {
		t.Errorf("got CoAP code %v; expected %v", rv.Code, coap.NotFound)
	}
	if rv.Option(coap.ContentFormat) != coap.AppXML {
		t.Errorf("got content format %v; expected %v", rv.Option(coap.ContentFormat), coap.AppXML)
	}
}

func TestProxyWithExternalServer(t *testing.T) {
	udpListener, crosscoapAddr := createLocalUDPListener(t)
	defer udpListener.Close()
	proxy := Proxy{Listener: udpListener, BackendURL: "https://s3.amazonaws.com/"}
	go proxy.Serve()

	req := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.GET,
		MessageID: 9876,
	}
	req.SetPathString("/non-existing-test-s3-bucket/coap-test/file")

	c, err := coap.Dial("udp", crosscoapAddr)
	if err != nil {
		t.Fatalf("Error dialing: %v", err)
	}
	rv, err := c.Send(req)
	if err != nil {
		t.Fatalf("Error sending request: %v", err)
	}
	if rv == nil {
		t.Fatalf("Didn't receive CoAP response")
	}

	if !strings.Contains(string(rv.Payload), "NoSuchBucket") {
		t.Errorf("got body %q which doesn't include the required string", string(rv.Payload))
	}
	if rv.Code != coap.NotFound {
		t.Errorf("got CoAP code %v; expected %v", rv.Code, coap.NotFound)
	}
	if rv.Option(coap.ContentFormat) != coap.AppXML {
		t.Errorf("got content format %v; expected %v", rv.Option(coap.ContentFormat), coap.AppXML)
	}
}

func createLocalUDPListener(t *testing.T) (*net.UDPConn, string) {
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("Can't resolve UDP addr")
	}
	udpListener, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal("Can't listen on UDP")
	}
	listenerAddr := udpListener.LocalAddr().String()
	return udpListener, listenerAddr
}
