package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/ibm-security-innovation/crosscoap"
)

var (
	listenAddr    = flag.String("listen", "0.0.0.0:5683", "CoAP listen address and port")
	backendURL    = flag.String("backend", "", "Backend HTTP server URL")
	errorLogName  = flag.String("errorlog", "", "Error log file name (default is stderr)")
	accessLogName = flag.String("accesslog", "", "Access log file name (default is no log)")
)

func main() {
	flag.Parse()
	if *backendURL == "" {
		flag.Usage()
		os.Exit(1)
	}

	var errorLog *log.Logger
	if *errorLogName == "" {
		errorLog = log.New(os.Stderr, "", log.LstdFlags)
	} else {
		errorLogFile, err := os.OpenFile(*errorLogName, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("Error opening error log file: %v", err)
		}
		defer errorLogFile.Close()
		errorLog = log.New(errorLogFile, "", log.LstdFlags)
	}

	var accessLog *log.Logger
	if *accessLogName != "" {
		accessLogFile, err := os.OpenFile(*accessLogName, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("Error opening access log file: %v", err)
		}
		defer accessLogFile.Close()
		accessLog = log.New(accessLogFile, "", log.LstdFlags)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", *listenAddr)
	if err != nil {
		errorLog.Fatalln("Can't resolve UDP addr")
	}
	udpListener, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		errorLog.Fatalln("Can't listen on UDP")
	}
	defer udpListener.Close()

	errorLog.Printf("crosscoap started: Listening for CoAP on UDP %v ...", *listenAddr)

	p := crosscoap.Proxy{
		Listener:   udpListener,
		BackendURL: *backendURL,
		ErrorLog:   errorLog,
		AccessLog:  accessLog,
	}
	err = p.Serve()
	if err != nil {
		errorLog.Fatalln(err)
	}
}
