package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Pod represents a proxy pod
type Pod struct {
	sync.RWMutex

	// IP represents the proxy pod's internal IP
	IP string

	// Timestamp represents the local timestamp of the proxy's last response
	Timestamp time.Time

	// Counter is a strictly increasing, pod local count for ordering requests
	// If it is -1, then the pod has been marked dead
	Counter int64

	// Free represents the predicted number of requests the pod can support before denying
	Free int64
}

// Proxy maintains the proxy url and proxy pods
type Proxy struct {
	sync.RWMutex

	// Service represents the service URL of any proxy, used as a backup
	Service *url.URL

	// Version represents the proxy's StatefulSet's resourceVersion
	Version int64

	// Pods represents the known proxy pods
	Pods map[int]*Pod

	// LastPodOrdinal represents the last known pod ordinal
	LastPodOrdinal int

	// Config represents the custom user configuration for this proxy struct
	Config Config
}

// Config provides extra control over the proxy
type Config struct {
	// NumberOfSenders represents the number of senders, including this one
	// This value is used for free count prediction
	NumberOfSenders uint

	// Attempts is an upper bound of attempts to make a proxy request before giving up
	Attempts uint

	// WebhookCallbackURL is the URL of a webhook to trigger when a request fails
	// on the proxy's end after the timeout has expired
	WebhookCallbackURL string

	// PingClient is the HTTP client to use for ping requests
	PingClient *http.Client

	// PingInterval is the time between each ping, default 1 second
	PingInterval time.Duration

	// DebugLevel is the debug verbosity level, default 0 (no debugging)
	DebugLevel int

	// DebugPrint is the debug print function used by the proxy methods for debugging
	DebugPrint func(string, ...interface{})
}

// New constructs a new proxy with the proxy service URL
// The proxy service URL's path and port will be used for subsequent proxy requests
func New(proxyServiceURL string) (*Proxy, error) {
	u, err := url.Parse(proxyServiceURL)
	if err != nil {
		return nil, err
	}

	proxy := &Proxy{
		Service: u,
		Pods:    map[int]*Pod{},
		Config: Config{
			NumberOfSenders: 1,
			Attempts:        math.MaxUint32,
			PingInterval:    time.Second,
		},
	}

	go proxy.pingProxies()

	return proxy, nil
}

// NewWithConfig constructs a new proxy with the proxy service URL and config
// The proxy service URL's path and port will be used for subsequent proxy requests
func NewWithConfig(proxyServiceURL string, config Config) (*Proxy, error) {
	proxy, err := New(proxyServiceURL)
	if err != nil {
		return nil, err
	}

	if config.NumberOfSenders == 0 {
		config.NumberOfSenders = proxy.Config.NumberOfSenders
	}

	if config.Attempts == 0 {
		config.Attempts = proxy.Config.Attempts
	}

	if config.PingInterval == 0 {
		config.PingInterval = proxy.Config.PingInterval
	}

	proxy.Config = config
	return proxy, nil
}

// Destroy cleans the proxy and kills the corresponding ping thread
func (p *Proxy) Destroy() {
	p.Service = nil
}

func (p *Proxy) debugPrint(level int, format string, args ...interface{}) {
	if p.Config.DebugPrint == nil || level > p.Config.DebugLevel {
		return
	}

	p.Config.DebugPrint(format, args...)
}

func (p *Proxy) formatURL(ip string) string {
	// Format the URL into scheme://ip:port/path
	return fmt.Sprintf("%v://%v:%v%v", p.Service.Scheme, ip, p.Service.Port(), p.Service.Path)
}

// Pings a specific proxy pod (performs a locking operation on success)
func (p *Proxy) pingProxy(proxyOrdinal int, proxyURL string) error {
	client := p.Config.PingClient
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Get(proxyURL)
	if err != nil {
		p.markProxyPodAsDead(proxyOrdinal)

		p.debugPrint(1, "Failed to ping proxy %v (%v): %v", proxyOrdinal, proxyURL, err)
		return err
	}

	defer resp.Body.Close()
	updateKnownProxies(p, &resp.Header)
	return nil
}

// Pings the proxies every second for metrics
func (p *Proxy) pingProxies() {
	for {
		if p.Service == nil {
			return
		}

		var wg sync.WaitGroup
		var successes int64

		p.RLock()

		// Go through each pod and ping it
		for i, proxyPod := range p.Pods {
			proxyPod.RLock()

			// Has it been more than a second since the last response?
			if time.Since(proxyPod.Timestamp) > time.Second {
				wg.Add(1)

				p.debugPrint(2, "Pinging proxy %v: %v", i, proxyPod.IP)

				// If so, ping it
				go func(proxyOrdinal int, proxyPod *Pod) {
					defer wg.Done()

					if p.pingProxy(proxyOrdinal, p.formatURL(proxyPod.IP)) == nil {
						atomic.AddInt64(&successes, 1)
					}
				}(i, proxyPod)
			} else {
				atomic.AddInt64(&successes, 1)
			}

			proxyPod.RUnlock()
		}

		p.RUnlock()
		wg.Wait()

		if successes == 0 {
			// If we got no successes, call determineBestProxy to possibly reset the list back to host
			p.Lock()
			proxyOrdinal, proxyHost, _ := p.determineBestProxy()
			p.Unlock()

			if proxyOrdinal == -1 {
				p.pingProxy(proxyOrdinal, proxyHost.String())
			}
		}

		time.Sleep(p.Config.PingInterval)
	}
}

// Determines the best proxy based on current metrics
func (p *Proxy) determineBestProxy() (int, *url.URL, error) {
	if len(p.Pods) == 0 {
		return -1, p.Service, nil
	}

	determineBestProxyOrdinal := func() int {
		bestOrdinal := -1
		bestFree := int64(-math.MaxInt64)

		// Pick the most free pod that isn't the last one
		for ordinal := 0; ordinal <= p.LastPodOrdinal; ordinal++ {
			pod, ok := p.Pods[ordinal]
			if !ok || pod.Counter < 0 {
				continue
			}

			if ordinal == p.LastPodOrdinal && bestFree > 0 {
				break
			}

			if pod.Free > bestFree {
				bestOrdinal = ordinal
				bestFree = pod.Free
			}
		}

		return bestOrdinal
	}

	ordinal := determineBestProxyOrdinal()

	// Is there no best proxy?
	if ordinal < 0 {
		p.debugPrint(1, "All pods dead, clearing pod list")

		// Clear the pod list and try the host
		p.Pods = map[int]*Pod{}
		p.LastPodOrdinal = 0
		return -1, p.Service, nil
	}

	u, err := url.Parse(p.formatURL(p.Pods[ordinal].IP))
	if err != nil {
		return 0, nil, err
	}

	return ordinal, u, nil
}

// Parses a proxy list header and returns the IP list
func parseProxyList(str string) (map[int]string, error) {
	var result map[int]string

	if err := json.Unmarshal([]byte(str), &result); err != nil {
		return result, err
	}

	return result, nil
}

// Returns whether the proxy pod list needs to be updated (was there a change?)
func (p *Proxy) shouldUpdateProxyList(newProxyList map[int]string, version int64) bool {
	// Don't update for the same version. Version changes on modified StatefulSet
	if version <= p.Version {
		return false
	}

	if len(p.Pods) != len(newProxyList) {
		return true
	}

	for ordinal, ip := range newProxyList {
		if pod, ok := p.Pods[ordinal]; !ok || ip != pod.IP {
			return true
		}
	}

	return false
}

// Marks a proxy pod as dead
func (p *Proxy) markProxyPodAsDead(proxyOrdinal int) {
	p.RLock()
	defer p.RUnlock()

	pod, ok := p.Pods[proxyOrdinal]
	if !ok {
		return
	}

	pod.Counter = -1
}

// Updates a specific proxy pod
func (p *Proxy) updateProxyPod(proxyOrdinal int, proxyCounter int64, proxyFree int64) {
	proxyPod, ok := p.Pods[proxyOrdinal]
	if !ok {
		return
	}

	// Is this data too old?
	if proxyCounter <= proxyPod.Counter {
		return
	}

	proxyPod.Lock()
	defer proxyPod.Unlock()

	// Check again
	if proxyCounter <= proxyPod.Counter {
		return
	}

	// Fill in data
	proxyPod.Counter = proxyCounter
	proxyPod.Free = proxyFree
	proxyPod.Timestamp = time.Now()
}

// Updates the proxy's dataset (performs a locking operation)
func updateKnownProxies(p *Proxy, header *http.Header) (int, error) {
	// Parse data from headers
	newProxyFree, err := strconv.ParseInt(header.Get("Proxy-Free"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-Free: %v", err)
	}

	proxyOrdinal, err := strconv.ParseInt(header.Get("Proxy-Ordinal"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-Ordinal: %v", err)
	}

	version, err := strconv.ParseInt(header.Get("Proxy-Version"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-Version: %v", err)
	}

	proxyCounter, err := strconv.ParseInt(header.Get("Proxy-Counter"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-Counter: %v", err)
	}

	proxyStatus, err := strconv.ParseInt(header.Get("Proxy-Status"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-Status: %v", err)
	}

	newProxyList, err := parseProxyList(header.Get("Proxy-List"))
	if err != nil {
		return 0, fmt.Errorf("error parsing Proxy-List: %v", err)
	}

	// Do we need to update the pod list?
	p.RLock()
	proxyListNeedsUpdate := p.shouldUpdateProxyList(newProxyList, version)
	p.RUnlock()

	// We need to update
	if proxyListNeedsUpdate {
		p.Lock()

		// Check if we are still the latest
		if p.Version < version {
			newPods := map[int]*Pod{}

			newLastPodOrdinal := 0
			for ordinal, newIP := range newProxyList {
				if ordinal > newLastPodOrdinal {
					newLastPodOrdinal = ordinal
				}

				if pod, ok := p.Pods[ordinal]; ok {
					if pod.IP == newIP {
						newPods[ordinal] = pod
						continue
					}
				}

				newPods[ordinal] = &Pod{IP: newIP}
			}

			p.Pods = newPods
			p.Version = version
			p.LastPodOrdinal = newLastPodOrdinal
		}

		p.Unlock()
	}

	// Update the pod
	p.RLock()
	p.updateProxyPod(int(proxyOrdinal), proxyCounter, newProxyFree)
	p.RUnlock()

	return int(proxyStatus), nil
}

// These errors occur in edge cases where the last proxy terminates just as the client gets a burst of messages
// This means the client thinks the last proxy is still alive when it actually isn't
func isRetryError(err error) bool {
	return strings.Contains(err.Error(), "connection timed out") || strings.Contains(err.Error(), "connection refused")
}

// Do forwards a non-blocking HTTP request to the proxy
func (p *Proxy) Do(client *http.Client, req *http.Request) (*http.Response, error) {
	for attempt := uint(1); ; attempt++ {
		p.Lock()

		// Determine the best proxy
		proxyOrdinal, proxyURL, err := p.determineBestProxy()
		if err != nil {
			p.Unlock()
			return nil, err
		}

		if proxyOrdinal >= 0 {
			// Decrement free count as a prediction
			atomic.AddInt64(&p.Pods[proxyOrdinal].Free, -1*int64(p.Config.NumberOfSenders))
		}

		p.debugPrint(3, "Sending request to proxy %v: %v", proxyOrdinal, proxyURL.String())
		p.Unlock()

		// Set headers
		req.Header.Set("Proxy-Forward-To", req.URL.String())
		req.Header.Set("Proxy-Webhook-Callback", p.Config.WebhookCallbackURL)

		// Pass along the client's TLS setting for the Proxy to use
		transport, ok := client.Transport.(*http.Transport)
		if ok && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
			req.Header.Set("Proxy-Insecure-Skip-Verify", "true")
		}

		// Do the actual request
		req.URL = proxyURL
		resp, err := client.Do(req)
		if err != nil {
			if proxyOrdinal >= 0 {
				p.markProxyPodAsDead(proxyOrdinal)

				// Retry if needed
				if attempt < p.Config.Attempts && isRetryError(err) {
					continue
				}
			}

			return nil, err
		}

		// Parse the response
		_, err = updateKnownProxies(p, &resp.Header)
		if err != nil {
			// Only fails if the proxy sends back invalid headers
			return nil, err
		}

		// Return response without proxy headers, except Proxy-Status
		resp.Header.Del("Proxy-Free")
		resp.Header.Del("Proxy-Ordinal")
		resp.Header.Del("Proxy-Version")
		resp.Header.Del("Proxy-List")
		return resp, nil
	}
}

// Ensure attempts to ensure there are enough proxies to handle the predicted incoming requests
func (p *Proxy) Ensure(client *http.Client, ensureRequests int) error {
	// Create the request
	req, err := http.NewRequest("POST", p.Service.String(), nil)
	if err != nil {
		return err
	}

	// Encode the Ensure-Request header
	req.Header.Set("Proxy-Ensure-Requests", strconv.Itoa(ensureRequests))

	p.debugPrint(2, "Sending ensure request to: %v", p.Service.String())

	// Do the request
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// Parse the response
	proxyStatus, err := updateKnownProxies(p, &resp.Header)
	if err != nil {
		// Only fails if the proxy sends back invalid headers
		return err
	}

	if proxyStatus == http.StatusOK {
		// Ensure request succeeded
		return nil
	}

	// Unexpected error with the request
	return errors.New("Unexpected proxy status code " + strconv.Itoa(proxyStatus))
}
