package crosscoap

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/dustin/go-coap"
)

type translatedCOAPMessage struct {
	coap.Message
}

func invertMap(src map[coap.MediaType]string) map[string]coap.MediaType {
	dst := make(map[string]coap.MediaType, len(src))
	for key, val := range src {
		dst[val] = key
	}
	return dst
}

var coapContentFormatContentType = map[coap.MediaType]string{
	coap.TextPlain:     "text/plain;charset=utf-8",
	coap.AppLinkFormat: "application/link-format",
	coap.AppXML:        "application/xml",
	coap.AppOctets:     "application/octet-stream",
	coap.AppExi:        "application/exi",
	coap.AppJSON:       "application/json",
}

var httpContentTypeContentFormat = invertMap(coapContentFormatContentType)

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

func translateContentType(contentType string) (coap.MediaType, bool) {
	parts := strings.SplitN(contentType, ";", 2)
	contentFormat, found := httpContentTypeContentFormat[parts[0]]
	return contentFormat, found
}

func getHTTPContentTypeFromCOAPMessage(msg coap.Message) string {
	contentFormat := msg.Option(coap.ContentFormat)
	if contentFormat != nil {
		contentType, found := coapContentFormatContentType[contentFormat.(coap.MediaType)]
		if found {
			return contentType
		}
	}
	return ""
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

	contentType := getHTTPContentTypeFromCOAPMessage(*coapMsg)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	for _, o := range coapMsg.VendorOptions() {
		parts := strings.Split(string(o.([]byte)), ":")
		if len(parts) < 2 {
			continue
		}

		req.Header.Add(parts[0], parts[1])
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
	}

	if httpError != nil {
		coapResp.Code = coap.ServiceUnavailable
		return &coapResp, nil
	}

	coapResp.Code = translateStatusCode(httpResp.StatusCode)
	contentFormat, hasContentFormat := translateContentType(httpResp.Header.Get("Content-Type"))
	if hasContentFormat {
		coapResp.SetOption(coap.ContentFormat, contentFormat)
	}

	_, err := coapResp.MarshalBinary()
	if err != nil {
		coapResp.Code = coap.InternalServerError
		coapResp.RemoveOption(coap.ContentFormat)
		return &coapResp, err
	}

	// Don't worry about packet size since go-coap supports Block2
	coapResp.Payload = httpBody

	return &coapResp, nil
}

func generateBadRequestCOAPResponse(coapRequest *coap.Message) *translatedCOAPMessage {
	return &translatedCOAPMessage{
		Message: coap.Message{
			Type:      coap.Acknowledgement,
			Code:      coap.BadRequest,
			MessageID: coapRequest.MessageID,
			Token:     coapRequest.Token,
		},
	}
}

func addFinalSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}
