package crosscoap

import (
	"bytes"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/energomonitor/go-coap"
)

const maxCOAPPacketLen = 1500

type translatedCOAPMessage struct {
	coap.Message
	IsTruncated bool
}

type content struct {
	Type     string
	Encoding string
}

const appJSONDeflate coap.MediaType = 11050

var coapContentFormatContentType = map[coap.MediaType]content{
	coap.TextPlain:     content{Type: "text/plain;charset=utf-8"},
	coap.AppLinkFormat: content{Type: "application/link-format"},
	coap.AppXML:        content{Type: "application/xml"},
	coap.AppOctets:     content{Type: "application/octet-stream"},
	coap.AppExi:        content{Type: "application/exi"},
	coap.AppJSON:       content{Type: "application/json"},
	appJSONDeflate:     content{Type: "application/json", Encoding: "deflate"},
}

var httpStatusCOAPCode = map[int]coap.COAPCode{
	http.StatusOK:        coap.Content,
	http.StatusCreated:   coap.Created,
	http.StatusNoContent: coap.Content,

	http.StatusNotModified: coap.Valid,

	http.StatusBadRequest:            coap.BadRequest,
	http.StatusUnauthorized:          coap.Unauthorized,
	http.StatusForbidden:             coap.Forbidden,
	http.StatusNotFound:              coap.NotFound,
	http.StatusMethodNotAllowed:      coap.MethodNotAllowed,
	http.StatusNotAcceptable:         coap.NotAcceptable,
	http.StatusPreconditionFailed:    coap.PreconditionFailed,
	http.StatusRequestEntityTooLarge: coap.RequestEntityTooLarge,
	http.StatusUnsupportedMediaType:  coap.UnsupportedMediaType,

	http.StatusInternalServerError: coap.InternalServerError,
	http.StatusNotImplemented:      coap.NotImplemented,
	http.StatusBadGateway:          coap.BadGateway,
	http.StatusServiceUnavailable:  coap.ServiceUnavailable,
	http.StatusGatewayTimeout:      coap.GatewayTimeout,
}

func translateStatusCode(httpStatusCode int) coap.COAPCode {
	coapCode, found := httpStatusCOAPCode[httpStatusCode]
	if found {
		return coapCode
	}
	return coap.Content
}

func trimCharset(val string) string {
	return strings.SplitN(val, ";", 2)[0]
}

func translateContentTypeWithEncoding(contentType, contentEncoding string) (coap.MediaType, bool) {
	contentType = trimCharset(contentType)
	for mediaType, ct := range coapContentFormatContentType {
		if trimCharset(ct.Type) == contentType && ct.Encoding == contentEncoding {
			return mediaType, true
		}
	}
	return 0, false
}

func getContentFormatFromCoapMessage(msg coap.Message) (content, bool) {
	contentFormat := msg.Option(coap.ContentFormat)
	if contentFormat != nil {
		ct, found := coapContentFormatContentType[contentFormat.(coap.MediaType)]
		return ct, found
	}
	return content{}, false
}

func escapeKeyValue(s string) string {
	kv := strings.SplitN(s, "=", 2)
	if len(kv) == 1 {
		return url.QueryEscape(kv[0])
	}
	return url.QueryEscape(kv[0]) + "=" + url.QueryEscape(kv[1])
}

func queryString(coapMsg *coap.Message) string {
	uriQueryOptions := coapMsg.Options(coap.URIQuery)
	parts := make([]string, 0, len(uriQueryOptions))
	for _, part := range uriQueryOptions {
		partStr, ok := part.(string)
		if !ok {
			continue
		}
		parts = append(parts, escapeKeyValue(partStr))
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

func translateCOAPRequestToHTTPRequest(coapMsg *coap.Message, backendURLPrefix string) *http.Request {
	method := coapMsg.Code.String()
	url := addFinalSlash(backendURLPrefix) + coapMsg.PathString() + queryString(coapMsg)
	body := bytes.NewReader(coapMsg.Payload)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil
	}

	if s, ok := coapMsg.Option(coap.URIHost).(string); ok {
		req.Host = s
	}

	contentFormat, found := getContentFormatFromCoapMessage(*coapMsg)

	if found {
		if contentFormat.Type != "" {
			req.Header.Set("Content-Type", contentFormat.Type)
		}
		if contentFormat.Encoding != "" {
			req.Header.Set("Content-Encoding", contentFormat.Encoding)
		}
	}
	return req
}

func translateHTTPResponseToCOAPResponse(httpResp *http.Response, httpBody []byte, httpError error, coapRequest *coap.Message) (*translatedCOAPMessage, error) {
	coapResp := translatedCOAPMessage{
		Message: coap.Message{
			Type:      coap.Acknowledgement,
			MessageID: coapRequest.MessageID,
			Token:     coapRequest.Token,
		},
		IsTruncated: false,
	}

	if httpError != nil {
		coapResp.Code = coap.ServiceUnavailable
		return &coapResp, nil
	}

	if coapRequest.IsBlock1() {
		num, szx, more := coapRequest.Block1()
		coapResp.SetBlock1(num, szx, more)
		if more {
			coapResp.Code = coap.Continue
			return &coapResp, nil
		}
	}
	if coapRequest.IsBlock2() {
		num, szx, _ := coapRequest.Block2()
		// Calculate the block size from size exponent
		size := uint32(math.Pow(2, float64(szx)))
		// Are there other blocks? Assume that there are...
		more := true
		// Read from this offset
		readFrom := int(num * size)
		// Read to this offset
		readTo := readFrom + int(size)
		if readFrom > len(httpBody) {
			// If offset lays beyond boundaries of the slice, read from the beginning
			readFrom = 0
		}
		if readTo > len(httpBody) {
			readTo = len(httpBody)
			// We have reached the end of body, there aren't any other blocks
			more = false
		}
		httpBody = httpBody[readFrom:readTo]
		coapResp.SetBlock2(num, szx, more)
	}

	coapResp.Code = translateStatusCode(httpResp.StatusCode)
	contentFormat, hasContentFormat := translateContentTypeWithEncoding(
		httpResp.Header.Get("Content-Type"),
		httpResp.Header.Get("Content-Encoding"))
	if hasContentFormat {
		coapResp.SetOption(coap.ContentFormat, contentFormat)
	}

	// intermediate marshalling
	packetHeaders, err := coapResp.MarshalBinary()
	if err != nil {
		coapResp.Code = coap.InternalServerError
		coapResp.RemoveOption(coap.ContentFormat)
		return &coapResp, err
	}

	// Check the size so far (+ 1 byte for the payload separator 0xff)
	headersLen := len(packetHeaders) + 1
	bytesLeft := maxCOAPPacketLen - headersLen
	if len(httpBody) > bytesLeft {
		coapResp.Payload = httpBody[:bytesLeft]
		coapResp.IsTruncated = true
	} else {
		coapResp.Payload = httpBody
	}
	return &coapResp, nil
}

func generateCOAPResponseMessage(coapRequest *coap.Message, statusCode coap.COAPCode) *coap.Message {
	return &coap.Message{
		Type:      coap.Acknowledgement,
		Code:      statusCode,
		MessageID: coapRequest.MessageID,
		Token:     coapRequest.Token,
	}
}

func generateBadRequestCOAPResponse(coapRequest *coap.Message) *translatedCOAPMessage {
	return &translatedCOAPMessage{
		Message: coap.Message{
			Type:      coap.Acknowledgement,
			Code:      coap.BadRequest,
			MessageID: coapRequest.MessageID,
			Token:     coapRequest.Token,
		},
		IsTruncated: false,
	}
}

func addFinalSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}
