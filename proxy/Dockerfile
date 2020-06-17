FROM golang:1.14
RUN go get "k8s.io/klog" && \
    go get "k8s.io/api/apps/v1" && \
    go get "k8s.io/api/autoscaling/v1" && \
    go get "k8s.io/api/core/v1" && \
    go get "k8s.io/apimachinery/pkg/apis/meta/v1" && \
    go get "k8s.io/apimachinery/pkg/watch" && \
    go get "k8s.io/client-go/kubernetes" && \
    go get "k8s.io/client-go/rest"
COPY ./main.go .
RUN go get -d && CGO_ENABLED=0 go build -ldflags "-w -extldflags -static" -tags netgo -installsuffix netgo -o ./proxy

FROM scratch
COPY --from=0 /go/proxy .
ENTRYPOINT [ "./proxy" ]