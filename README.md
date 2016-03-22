# crosscoap

**crosscoap** is a CoAP ([Constrained Application Protocol][1]) UDP server
which translates incoming CoAP requests to corresponding HTTP requests which
are sent to a backend HTTP server.  The HTTP responses from the backend are
translated back to CoAP and sent over to the CoAP client.

**crosscoap** can be used in front of any HTTP server to quickly allow CoAP
clients to consume content from that HTTP application, without adding specific
CoAP functionality to the application itself.

[1]: https://en.wikipedia.org/wiki/Constrained_Application_Protocol


## Install

[Go][2] must be installed and `$GOPATH` properly set.  To install crosscoap,
run:

    go get github.com/ibm-security-innovation/crosscoap/...

This should build the `crosscoap` executable in `$GOPATH/bin`.

crosscoap relies on the [go-coap][3] library to handle the CoAP packets;
it is installed as part of the `go get`.

[2]: https://golang.org/doc/install
[3]: https://github.com/dustin/go-coap


## Command usage

To proxy incoming CoAP packets (UDP port 5683) to a local HTTP server listening
on port 8000:

    crosscoap -listen 0.0.0.0:5683 -backend http://127.0.0.1:8000/api

Command-line switches:

* `-listen LISTEN_ADDR_PORT`: The address and UDP port on which to listen for
  incoming CoAP UDP requests (example: `0.0.0.0:5683`)
* `-backend BACKEND_URL`: The URL of the HTTP backend server (example:
  `http://127.0.0.1:8000/api/v1`)
* `-errorlog FILENAME`: Log errors to file (default is logging errors to
  stderr) (example: `/tmp/crosscoap-error.log`)
* `-accesslog`: Log every request to file (example: `/tmp/crosscoap-access.log`)


### Example: fetching Mars weather data over CoAP

The following command will start a CoAP server on UDP port 5683; incoming
requests will be translated to HTTP and proxied to the [Mars Weather MAAS API][4]
service:

    crosscoap -listen 0.0.0.0:5683 -backend http://marsweather.ingenology.com/v1 -accesslog /tmp/coap-mars-access.log

Once this is running, you can get the latest Mars weather information with a
CoAP client.  Here is an example with [coap-cli][5] (line breaks were added to
the response for clarity):

    coap get coap://localhost/latest

    (2.05) {"report":
            {"terrestrial_date": "2015-10-12",
             "sol": 1131,
             "ls": 53.0,
             "min_temp": -81.0,
             "min_temp_fahrenheit": -113.8,
             "max_temp": -28.0,
             "max_temp_fahrenheit": -18.4,
             "pressure": 902.0,
             "pressure_string": "Higher",
             "abs_humidity": null,
             "wind_speed": null,
             "wind_direction": "--",
             "atmo_opacity": "Sunny",
             "season": "Month 2",
             "sunrise": "2015-10-12T11:09:00Z",
             "sunset": "2015-10-12T22:55:00Z"}}

In this example, the CoAP request to `/latest` was proxied to the HTTP backend
as `http://marsweather.ingenology.com/v1/latest`. The backend HTTP server's
`200 OK` response was translated back to a CoAP 2.05 (Content) response with
the CoAP payload holding the JSON document.

[4]: http://marsweather.ingenology.com/
[5]: https://github.com/mcollina/coap-cli


## Library usage

crosscoap may be used as a Go package in order to add the CoAP proxying
functionality to an existing Go application. For example:

    package main

    import (
            "log"

            "github.com/ibm-security-innovation/crosscoap"
    )

    func main() {
            appLog := log.New(os.Stderr, "", log.LstdFlags)
            udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:5683")
            if err != nil {
                    appLog.Fatalln("Can't resolve UDP addr")
            }
            udpListener, err := net.ListenUDP("udp", udpAddr)
            if err != nil {
                    errorLog.Fatalln("Can't listen on UDP")
            }
            defer udpListener.Close()
            p := crosscoap.COAPProxy{
                    Listener:   udpListener,
                    BackendURL: "http://127.0.0.1:8000/",
                    Timeout:    10 * time.Second,
                    AccessLog:  appLog,
                    ErrorLog:   appLog,
            }
            appLog.Fatal(p.Serve())
    }


## Notes

CoAP and HTTP are different protocols.  crosscoap attempts to translate status
codes and headers (options in CoAP) between the two protocols, but not every
code and header has a corresponding entity in the other protocol.

CoAP is UDP-based, and therefore the entire CoAP message (UDP headers, CoAP
headers and CoAP payload) must fit in the network MTU, which is generally
around 1500 bytes.  If the response body from the HTTP server is too long,
crosscoap will truncate it (and log an error).  Keep the HTTP response body
under 1400 bytes to be safe.


## Related documentation

* [Project homepage](https://developer.ibm.com/open/crosscoap/)
* [RFC 7252: The Constrained Application Protocol (CoAP)](https://tools.ietf.org/html/rfc7252)
* [Guidelines for HTTP-CoAP Mapping Implementations (draft)](https://tools.ietf.org/html/draft-ietf-core-http-mapping-07)


## Alternatives

* [Eclipse Californium (Cf) CoAP framework](https://github.com/eclipse/californium.core)
* [jCoAP](https://code.google.com/p/jcoap/)


## License

(c) Copyright IBM Corp. 2015, 2016.

This project is licensed under the Apache License 2.0.  See the
[LICENSE](LICENSE) file for more info.

3rd party software used by crosscoap:

* [go-coap](https://github.com/dustin/go-coap):  MIT licesne
* [The Go Programming Language](https://golang.org): BSD-style license


# Contribution

Contributions to the project are welcomed.  It is required however to provide
alongside the pull request one of the contribution forms (CLA) that are a part
of the project.  If the contributor is operating in his individual or personal
capacity, then he/she is to use the [individual CLA](./CLA-Individual.txt); if
operating in his/her role at a company or entity, then he/she must use the
[corporate CLA](CLA-Corporate.txt).
