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

	"github.com/energomonitor/go-coap"
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

func (p *proxyHandler) cacheHTTPRequest(req *http.Request) (*http.Request, error) {
	cacheKey := fmt.Sprintf("REQ %s %s", req.Method, req.URL.String())
	// Find the request in cache
	httpReqRaw, cached := p.HTTPCache.Get(cacheKey)
	cacheReq := req
	if cached {
		var err error
		cacheReq, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(httpReqRaw)))
		if err != nil {
			return nil, err
		}
		// RequestURI is the unmodified Request-URI of the Request-Line as sent by the client to a server.
		// Usually the URL field should be used instead.
		// It is an error to set this field in an HTTP client request.
		cacheReq.RequestURI = ""
		// Request does not carry information about request URL so we must copy it from the last request.
		cacheReq.URL = req.URL

		// Read cached body
		httpBodyCached, err := ioutil.ReadAll(cacheReq.Body)
		if err != nil {
			return nil, err
		}
		cacheReq.Body.Close()

		// Read new body
		httpBody, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()

		// Update cached request's body and ContentLength!
		cacheReq.ContentLength = int64(len(httpBodyCached) + len(httpBody))
		cacheReq.Body = ioutil.NopCloser(bytes.NewReader(append(httpBodyCached, httpBody...)))
	}

	httpReqRaw, err := httputil.DumpRequestOut(cacheReq, true)
	if err != nil {
		return nil, err
	}
	p.HTTPCache.Set(cacheKey, httpReqRaw)
	return cacheReq, err
}

func (p *proxyHandler) doHTTPRequestCached(req *http.Request) (*http.Response, []byte, error) {
	cacheKey := fmt.Sprintf("RES %s %s", req.Method, req.URL.String())
	httpRespRaw, cached := p.HTTPCache.Get(cacheKey)
	if cached {
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
	// ioutil.ReadAll above, consumes entire Body so in order to have the body
	// readable again, we must recreate it.
	httpResp.Body = ioutil.NopCloser(bytes.NewReader(httpBody))
	return httpResp, httpBody, nil
}

func (p *proxyHandler) ServeCOAP(l *net.UDPConn, a *net.UDPAddr, m *coap.Message) *coap.Message {
	p.logAccess("%v: CoAP %v URI-Path=%v URI-Query=%v", a, m.Code, m.PathString(), m.Options(coap.URIQuery))
	waitForResponse := m.IsConfirmable()
	req := translateCOAPRequestToHTTPRequest(m, p.BackendURL)
	if req == nil {
		return generateCOAPResponseMessage(m, coap.BadRequest)
	}
	req.Header.Set("User-Agent", userAgent)

	responseChan := make(chan *coap.Message, 1)
	// Helper function to send coap response from goroutine to responseChan.
	sendResponse := func(m *coap.Message) {
		// The select prevents blocking if there aren't any readers.
		select {
		case responseChan <- m:
		default:
		}
	}
	go func() {
		var httpResp *http.Response
		var httpBody []byte
		var delayRequest bool
		var err error
		if m.IsBlock1() {
			var err error
			req, err = p.cacheHTTPRequest(req)
			if err != nil {
				p.logError("Error on cache HTTP request: %v", err)
				sendResponse(generateCOAPResponseMessage(m, coap.InternalServerError))
				return
			}
			_, _, delayRequest = m.Block1()
		}

		// Do HTTP request.
		// There are two ways a request is done. Either with response caching enabled or without caching.
		// Response caching is enabled when a CoAP client sends a request with Block2 option.
		// There's also a case when HTTP request is postponed. This happens when clients sends a CoAP request
		// with Block1 option. In this case, incoming CoAP request is translated into HTTP request, the result of
		// the translation is then stored in cache until the last block is received. After the last block is received
		// only then the HTTP request can be sent.
		doHTTPRequestFunc := p.doHTTPRequest
		if m.IsBlock2() {
			doHTTPRequestFunc = p.doHTTPRequestCached
		}
		if !delayRequest {
			httpResp, httpBody, err = doHTTPRequestFunc(req)
			if err != nil {
				p.logError("Error on HTTP request: %v", err)
				sendResponse(generateCOAPResponseMessage(m, coap.InternalServerError))
				return
			}
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
		return generateCOAPResponseMessage(m, coap.Content)
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
