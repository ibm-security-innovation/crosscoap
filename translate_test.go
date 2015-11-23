package crosscoap

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/dustin/go-coap"
)

func getHTTPRespAndBody(t *testing.T, responseText string) (*http.Response, []byte) {
	responseReader := bufio.NewReader(bytes.NewReader([]byte(responseText)))
	httpResp, err := http.ReadResponse(responseReader, nil)
	if err != nil {
		t.Fatalf("Error creating test HTTP request: %v", err)
	}
	httpBody, err := ioutil.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("Error reading body from test HTTP request: %v", err)
	}
	return httpResp, httpBody
}

func TestTranslateCOAPRequestWithoutContentFormat(t *testing.T) {
	coapMsg := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.POST,
		MessageID: 1234,
	}
	coapMsg.SetPathString("/path/to/resource")
	coapMsg.SetOption(coap.URIQuery, []string{"a=b", "c=d e&=f"})
	coapMsg.Payload = []byte("The request body")

	httpReq := translateCOAPRequestToHTTPRequest(&coapMsg, "http://localhost:9876/backend1")
	if httpReq.Method != "POST" {
		t.Errorf("httpReq.Method is '%v'", httpReq.Method)
	}
	if httpReq.URL.String() != "http://localhost:9876/backend1/path/to/resource?a=b&c=d+e%26%3Df" {
		t.Errorf("httpReq.URL is '%v'", httpReq.URL)
	}
	if httpReq.Header.Get("Content-Type") != "" {
		t.Error("Expected Content-Type to be empty")
	}
	defer httpReq.Body.Close()
	body, err := ioutil.ReadAll(httpReq.Body)
	if err != nil {
		t.Error("Error reading httpReq.Body")
	}
	if string(body) != "The request body" {
		t.Errorf("httpReq.Body is '%v'", string(body))
	}
}

func TestTranslateCOAPRequestWithContentFormat(t *testing.T) {
	coapMsg := coap.Message{
		Type:      coap.Confirmable,
		Code:      coap.GET,
		MessageID: 1234,
	}
	coapMsg.SetPathString("resource")
	coapMsg.SetOption(coap.ContentFormat, coap.TextPlain)

	httpReq := translateCOAPRequestToHTTPRequest(&coapMsg, "http://localhost:9876/backend2/")
	if httpReq.Method != "GET" {
		t.Errorf("httpReq.Method is '%v'", httpReq.Method)
	}
	if httpReq.URL.String() != "http://localhost:9876/backend2/resource" {
		t.Errorf("httpReq.URL is '%v'", httpReq.URL)
	}
	if httpReq.Header.Get("Content-Type") != "text/plain;charset=utf-8" {
		t.Errorf("Content-Type is '%v'", httpReq.Header.Get("Content-Type"))
	}
}

func TestTranslateCOAPResponse(t *testing.T) {
	coapReq := coap.Message{MessageID: 1234, Token: []byte("MY-TOKEN")}
	coapReq.SetPathString("/path/to/resource")

	responseText := "HTTP/1.0 200 OK\r\n" +
		"Content-Type: application/json\r\n" +
		"\r\n" +
		`{"ok":"The response body"}`
	httpResp, httpBody := getHTTPRespAndBody(t, responseText)
	coapResp, err := translateHTTPResponseToCOAPResponse(httpResp, httpBody, nil, &coapReq)
	if err != nil {
		t.Fatalf("Error translating: %v", err)
	}
	if coapResp.Code != coap.Content {
		t.Errorf("coapResp.Code is '%v'", coapResp.Code)
	}
	if coapResp.IsTruncated {
		t.Error("Expected CoAP response to be non-truncated")
	}
	if string(coapResp.Payload) != `{"ok":"The response body"}` {
		t.Errorf("coapResp.Payload is '%v'", string(coapResp.Payload))
	}
	if coapResp.Option(coap.ContentFormat) != coap.AppJSON {
		t.Errorf("content format is %v", coapResp.Option(coap.ContentFormat))
	}
	if coapResp.MessageID != 1234 {
		t.Errorf("coapResp.MessageID is %v", coapResp.MessageID)
	}
	if !bytes.Equal(coapResp.Token, coapReq.Token) {
		t.Errorf("coapResp.Token is %v", coapResp.Token)
	}
}

func TestTranslateCOAPNoContentResponse(t *testing.T) {
	coapReq := coap.Message{MessageID: 1234}
	coapReq.SetPathString("/path/to/resource")

	responseText := "HTTP/1.0 204 No Content\r\n\r\n"
	httpResp, httpBody := getHTTPRespAndBody(t, responseText)
	coapResp, err := translateHTTPResponseToCOAPResponse(httpResp, httpBody, nil, &coapReq)
	if err != nil {
		t.Fatalf("Error translating: %v", err)
	}
	if coapResp.Code != coap.Content {
		t.Errorf("coapResp.Code is '%v'", coapResp.Code)
	}
	if string(coapResp.Payload) != "" {
		t.Errorf("coapResp.Payload is '%v'", string(coapResp.Payload))
	}
	if coapResp.Option(coap.ContentFormat) != nil {
		t.Errorf("content format is %v", coapResp.Option(coap.ContentFormat))
	}
	if coapResp.MessageID != 1234 {
		t.Errorf("coapResp.MessageID is %v", coapResp.MessageID)
	}
}

func TestTranslateCOAPResponseWithErrorDuringRequest(t *testing.T) {
	coapReq := coap.Message{MessageID: 1234}
	coapReq.SetPathString("/path/to/resource")
	coapResp, err := translateHTTPResponseToCOAPResponse(nil, nil, fmt.Errorf("dummy error"), &coapReq)
	if err != nil {
		t.Fatalf("Error translating: %v", err)
	}
	if coapResp.Code != coap.ServiceUnavailable {
		t.Errorf("coapResp.Code is '%v'", coapResp.Code)
	}
	if coapResp.Payload != nil {
		t.Errorf("coapResp.Payload is '%v'", coapResp.Payload)
	}
	if coapResp.Option(coap.ContentFormat) != nil {
		t.Errorf("content format is '%v'", coapResp.Option(coap.ContentFormat))
	}
	if coapResp.MessageID != 1234 {
		t.Errorf("coapResp.MessageID is '%v'", coapResp.MessageID)
	}
}

func TestTranslateCOAPResponseTrunactesBigHTTPBody(t *testing.T) {
	coapReq := coap.Message{MessageID: 1234, Token: []byte("TOKEN")}
	coapReq.SetPathString("/path/to/resource")

	responseText := "HTTP/1.0 200 OK\r\n" +
		"\r\n" +
		strings.Repeat("ABCD", 1000)
	httpResp, httpBody := getHTTPRespAndBody(t, responseText)
	coapResp, err := translateHTTPResponseToCOAPResponse(httpResp, httpBody, nil, &coapReq)
	if err != nil {
		t.Fatalf("Error translating: %v", err)
	}
	if !coapResp.IsTruncated {
		t.Error("Expected CoAP response to be truncated")
	}
	payload := string(coapResp.Payload)
	if exp := 1490; len(payload) != exp {
		t.Errorf("Expected CoAP payload %v, got %v", exp, len(payload))
	}
}
