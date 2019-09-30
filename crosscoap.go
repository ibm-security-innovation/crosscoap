// Package crosscoap implements a proxy+translator server that listens for
// incoming CoAP requests, translates them to HTTP requests which are proxied
// to the backend, and translates the respones back to CoAP (if the CoAP client
// request was confirmable).
//
// Example:
//
// 	package main
//
// 	import (
// 		"log"
// 		"net"
// 		"os"
// 		"time"
//
// 		"github.com/ibm-security-innovation/crosscoap"
// 	)
//
// 	func main() {
// 		timeout := time.Duration(10 * time.Second)
// 		appLog := log.New(os.Stderr, "[example] ", log.LstdFlags)
// 		udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:5683")
// 		if err != nil {
// 			appLog.Fatalln("Can't resolve UDP addr")
// 		}
// 		udpListener, err := net.ListenUDP("udp", udpAddr)
// 		if err != nil {
// 			errorLog.Fatalln("Can't listen on UDP")
// 		}
// 		defer udpListener.Close()
// 		p := crosscoap.Proxy{
// 			Listener:   udpListener,
// 			BackendURL: "http://127.0.0.1:8000/",
// 			Timeout:    &timeout,
// 			AccessLog:  appLog,
// 			ErrorLog:   appLog,
// 		}
// 		appLog.Fatal(p.Serve())
// 	}
//
package crosscoap

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/die-net/lrucache"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/besedad/go-coap"
)

// Proxy is CoAP server that takes an incoming CoAP request, translates it to
// an HTTP resquest and sends it to a backend HTTP server; the response it
// translated back to CoAP and returned to the original client.
type Proxy struct {
	// A UDP listener that will accept the incoming CoAP requests.
	Listener *net.UDPConn

	// URL of the HTTP (or HTTPS) backend server to which requests will be
	// proxied.
	BackendURL string

	// Timeout for requests to the HTTP backend.  If nil, a default of 5
	// seconds is used.
	Timeout *time.Duration

	// AccessLog specifies an optional logger which records each incoming
	// request received by the proxy.  If nil, requests are not logged.
	AccessLog *log.Logger

	// ErrorLog specifies an optional logger for errors that occur when
	// attempting to proxy the request.  If nil, error logging goes to
	// os.Stderr via the log package's standard logger.
	ErrorLog *log.Logger

	// HTTP cache stores responses for given period.
	HTTPCache *lrucache.LruCache
}

type proxyHandler struct {
	Proxy
}

const (
	defaultHTTPTimeout = 5 * time.Second
	userAgent          = "crosscoap/1.0"
)

func (p *proxyHandler) doHTTPRequestCached(req *http.Request) (*http.Response, []byte, error) {
	cacheKey := fmt.Sprintf("%s %s", req.Method, req.URL.String())
	httpRespRaw, cached := p.HTTPCache.Get(cacheKey)
	if cached {
		log.Printf("Found key `%s` in cache...", cacheKey)
		// Parse raw response data into http.Response struct
		httpResp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(httpRespRaw)), nil)
		if err != nil {
			return nil, nil, err
		}
		defer httpResp.Body.Close()
		httpBody, err := ioutil.ReadAll(httpResp.Body)
		if err != nil {
			return nil, nil, err
		}
		// Update cached key so it won't expire
		p.HTTPCache.Set(cacheKey, httpRespRaw)
		return httpResp, httpBody, nil
	}
	httpResp, httpBody, err := p.doHTTPRequest(req)
	if err != nil {
		return nil, nil, err
	}
	httpRespRaw, err = httputil.DumpResponse(httpResp, true)
	if err != nil {
		return nil, nil, err
	}
	// Store response in the cache
	p.HTTPCache.Set(cacheKey, httpRespRaw)
	return httpResp, httpBody, nil
}

func (p *proxyHandler) doHTTPRequest(req *http.Request) (*http.Response, []byte, error) {
	timeout := defaultHTTPTimeout
	if p.Timeout != nil {
		timeout = *p.Timeout
	}
	httpClient := &http.Client{Timeout: timeout}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer httpResp.Body.Close()
	httpBody, err := ioutil.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, err
	}
	// ioutil.ReadAll above, consumes entire Body so in order to have the body readable again, we must recreate it.
	httpResp.Body = ioutil.NopCloser(bytes.NewReader(httpBody))
	return httpResp, httpBody, nil
}

func (p *proxyHandler) ServeCOAP(l *net.UDPConn, a *net.UDPAddr, m *coap.Message) *coap.Message {
	p.logAccess("%v: CoAP %v URI-Path=%v URI-Query=%v", a, m.Code, m.PathString(), m.Options(coap.URIQuery))
	waitForResponse := m.IsConfirmable()
	req := translateCOAPRequestToHTTPRequest(m, p.BackendURL)
	if req == nil {
		if waitForResponse {
			return &generateBadRequestCOAPResponse(m).Message
		} else {
			return nil
		}
	}
	req.Header.Set("User-Agent", userAgent)
	responseChan := make(chan *coap.Message, 1)
	go func() {
		var httpResp *http.Response
		var httpBody []byte
		var err error
		// Test if Block2 is sent in request, if so look for response in cache
		if m.IsBlock2() {
			log.Printf("COAP Block2 found, caching the response...")
			httpResp, httpBody, err = p.doHTTPRequestCached(req)
		} else {
			httpResp, httpBody, err = p.doHTTPRequest(req)
		}
		if err != nil {
			p.logError("Error on HTTP request: %v", err)
		}
		if waitForResponse {
			coapResp, err := translateHTTPResponseToCOAPResponse(httpResp, httpBody, err, m)
			if err != nil {
				p.logError("Error translating HTTP to CoAP: %v", err)
			}
			if coapResp.IsTruncated {
				p.logError("CoAP payload truncated from %v bytes to %v bytes", len(httpBody), len(coapResp.Payload))
			}
			responseChan <- &coapResp.Message
		}
	}()

	if waitForResponse {
		coapResp := <-responseChan
		return coapResp
	} else {
		return nil
	}
}

func (p *Proxy) logAccess(format string, args ...interface{}) {
	if p.AccessLog == nil {
		return
	}
	p.AccessLog.Printf(format, args...)
}

func (p *Proxy) logError(format string, args ...interface{}) {
	if p.ErrorLog != nil {
		p.ErrorLog.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Serve starts accepting CoAP requests on the proxy's UDP listener
// (p.Listener); it never returns (unless there's an error accepting UDP
// packets or reading them).  The server starts a new goroutine to for each
// incoming UDP CoAP request.
func (p *Proxy) Serve() error {
	return coap.Serve(p.Listener, &proxyHandler{*p})
}

// ListenAndServe listens for incoming CoAP requests on the given protocol and
// address and proxy them to the HTTP server backendURL.
func ListenAndServe(protocol, addr, backendURL string) error {
	p := Proxy{BackendURL: backendURL}
	return coap.ListenAndServe(protocol, addr, &proxyHandler{p})
}
