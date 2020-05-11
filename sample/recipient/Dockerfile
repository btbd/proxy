FROM golang:1.14
COPY ./main.go .
RUN go get -d && go build -ldflags "-w -extldflags -static" -tags netgo -installsuffix netgo -o ./recipient

FROM scratch
COPY --from=0 /go/recipient .
ENTRYPOINT [ "./recipient" ]