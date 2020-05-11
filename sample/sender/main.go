package main

import (
	"log"
	"net/http"
	"sync/atomic"
	"time"

	proxy "github.com/btbd/proxy/client"
)

// ProxyURL is the service URL of any proxy
const ProxyURL = "http://proxy.default.svc.cluster.local"

// RecipientURL is the target URL to send requests to
const RecipientURL = "http://recipient.default.svc.cluster.local"

func main() {
	// Construct a new proxy sender
	proxy, err := proxy.NewWithConfig(ProxyURL, proxy.Config{
		NumberOfSenders: 1,
		DebugLevel:      1,
		DebugPrint:      log.Printf,
	})

	if err != nil {
		log.Fatalln(err)
	}

	var client http.Client
	var count int64

	// Ensure the proxies can handle 20 requests
	// This causes the proxies to forcibly scale up
	// proxy.Ensure(&client, 20)

	for {
		// Maintain 20 continuous requests
		for count >= 20 {
			time.Sleep(time.Millisecond)
		}

		atomic.AddInt64(&count, 1)
		go func() {
			defer atomic.AddInt64(&count, -1)

			req, err := http.NewRequest("GET", RecipientURL, nil)
			if err != nil {
				log.Fatalln(err)
			}

			resp, err := proxy.Do(&client, req)
			if err != nil {
				log.Println(err)
				return
			}

			// Log the status code
			// 429: denied, too full
			// 202: processed, no response
			// 200: got the full response from recipient
			// Take a look at resp.Header.Get("Proxy-Status") for more info
			log.Printf("%v\n", resp.StatusCode)
		}()
	}

	// Destroy the proxy and background ping routine
	// proxy.Destroy()
}
