package crosscoap

import (
	"github.com/besedad/go-coap"
	"github.com/die-net/lrucache"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyWithConfirmableRequest(t *testing.T) {
	const customUriHost = "hocus-pocus.example.com"
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
		if r.Host != customUriHost {
			t.Errorf("backend got unexpected Host: %v", r.Host)
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
	req.SetOption(coap.URIHost, customUriHost)

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

func TestProxyWithBadCOAPPacket(t *testing.T) {
	udpListener, crosscoapAddr := createLocalUDPListener(t)
	defer udpListener.Close()
	proxy := Proxy{Listener: udpListener, BackendURL: "https://s3.amazonaws.com/"}
	go proxy.Serve()

	req := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.GET,
		MessageID: 9876,
	}
	req.SetPathString("%")

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
	if rv.Code != coap.BadRequest {
		t.Errorf("got CoAP code %v; expected %v", rv.Code, coap.BadRequest)
	}
}

func TestProxyWithBlock2Cache(t *testing.T) {
	const backendStatus = http.StatusOK
	const backendResponse = "If you are reading this, it works."
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(backendStatus)
		w.Write([]byte(backendResponse))
	}))
	defer backend.Close()

	httpCache := lrucache.New(10*1024, 600)
	udpListener, crosscoapAddr := createLocalUDPListener(t)
	defer udpListener.Close()
	proxy := Proxy{
		Listener:   udpListener,
		BackendURL: backend.URL,
		HTTPCache:  httpCache,
	}
	go proxy.Serve()

	req := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.GET,
		MessageID: 12345,
	}
	req.SetPathString("/test")

	c, err := coap.Dial("udp", crosscoapAddr)
	if err != nil {
		t.Fatalf("Error dialing: %v", err)
	}

	// Download all payload blocks
	payload := []byte{}
	for i := uint32(4); true; i++ {
		req.SetBlock2(i, 4, false)

		rv, err := c.Send(req)
		if err != nil {
			t.Fatalf("Error sending request: %v", err)
		}
		if rv == nil {
			t.Fatalf("Didn't receive CoAP response")
		}
		if rv.Code != coap.Content {
			t.Errorf("got CoAP code %v; expected %v", rv.Code, coap.Content)
		}
		payload = append(payload, rv.Payload...)

		_, _, more := rv.Block2()
		if more == false {
			// There aren't any more blocks
			break
		}
	}

	if string(payload) != backendResponse {
		t.Errorf("got body %q; expected %q", string(payload), backendResponse)
	}

	_, ok := httpCache.Get("RES GET " + backend.URL + "/test")
	if ok == false {
		t.Errorf("HTTP Cache is empty, expects one item")
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
