# proxy [![Go Report Card](https://goreportcard.com/badge/github.com/btbd/proxy)](https://goreportcard.com/report/github.com/btbd/proxy)

Nonblocking, autoscaling, self-contained Kubernetes proxy.

## Purpose

Suppose you have one, or more, components trying to send
HTTP requests to services. In cases where those requests are "events", the
services should accept the event and return a `202` immediately since typically
events are one-way messages. However, not all receivers will do so. Many will
hold on to the incoming HTTP connection until they have completely finished
processing the event. Doing this causes back-pressure on the sender because
there will be a limited number of outbound connections any one sender can
support at a time.

This means that as the number of lingering outbound connections
grows, the number of events the sender will send decreases - impacting it's
overall throughput.

One option here is to scale the senders up as the number of lingering
connections grows. This presents a challenge in some environments where
scaling of the sender might cause an interruption in the flow of processing events. Or, there could simply be cases where the sender can't, or just doesn't want to, scale itself.

In a situation where neither the sender or receiver can be radically changed, this
proxy proves to be useful.

This proxy will receive requests from a sender, open a connection to the
final destination, wait a certain amount of time (e.g. 200ms) and if no
response is received, it'll then send back a `202` to the sender, but keep the
outbound connection to the service intact. This allows for the sender to
assume there will always be a well-behaving receiver (doesn't block)
and thus keep their overall throughput at their desired levels.

In addition, this proxy autoscales and maintains itself without any metrics hub, so it is both easily portable and scalable.

## Directory Structure

The proxy is split into two packages:
- `client/` - Client HTTP library that communicates with the proxies
- `proxy/` - Actual K8s StatefulSet proxy

There is also a sample:
- `sample/recipient` - Recipient that doesn't respond instantly
- `sample/sender` - Sender utilizing the proxy HTTP library

## Usage

The proxy itself is just a StatefulSet that can be deployed normally, see [proxy](proxy/).

Proxy configuration can be changed in the StatefulSet's
[annotations](proxy/proxy.yaml#L45):
- `minProxies` is the minimum number of proxies to be running at any given time.
- `maxProxies` is the maximum number of proxies to be running at any given time.
- `maxRequests` is the maximum number of active requests any proxy can be
   processing.
- `maxLoadFactor` is a scaling factor representing the target number of active
   requests a proxy should process before scaling.
   - For example, a `maxLoadFactor` of 0.5 with 100 `maxRequests` means when
     the last proxy exceeds `50` requests, it will attempt to scale up. The
	 effect `maxLoadFactor` has is creating a buffer request region in
	 between the time it takes K8s to spin up a new proxy and the new
	 incoming requests.
- `proxyTimeout` is the time in milliseconds a proxy should wait for a
   response from the recipient. If this timeout is reached without a
   response, the proxy returns a `202` to the sender.
- `idleTimeout` is the time in seconds the last proxy should wait before
   scaling itself down due to inactivity.
- `debugLevel` is the debug verbosity level.

The annotations can be changed in real-time. Meaning one can do
`kubectl edit <STATEFULSET>`, change one of these configs, and the proxies
will immediately reflect these changes.

## Design

- A proxy will return a `202` if it can connect to the destination but no response
  is returned within the `proxyTimeout` value (in milliseconds).
- A proxy will return a `429` if it can not process an incoming request due to
  it reaching the maximum number of outbound connections (`maxRequests`). While ideally this should not happen, it takes a few seconds for Kubernetes to create another proxy. So if there is a sudden burst of incoming requests, then a proxy may not be able to handle the load. This is why `maxLoadFactor` should be tuned to create an optimal buffer region.
  - The sender can also warn the proxies of the burst (through the client's `Ensure` function), so the proxies can scale up in preparation.
- Scaling up is not based on the maximum number of outbound requests per proxy, but rather the maximum multiplied by the `maxLoadFactor` percentage to create a buffer region.
- The client library will choose the least busy proxy instance, but will
  avoid the most recently created proxy when possible. This allows that last proxy
  to scale itself down when it is not required to sustain throughput due to the idle timeout.
- The client library bases its routing decisions on statistics
  returned from the proxies on each response or from a "ping". Pings are only sent if no requests have been sent to a proxy for a certain amount of time, so the client is aware of any down-scaling.
- The proxies do not have any retry logic. Any failure, from the final
  destination or from within the proxy, will be returned to the client to
  decide whether a retry is needed.
