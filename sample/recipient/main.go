package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Basic HTTP handler
func httpHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	time.Sleep(200 * time.Millisecond)

	w.Write([]byte("Hello, world!"))
}

func main() {
	http.HandleFunc("/", httpHandler)

	log.Println("Listening on port 80")
	log.Fatalln(http.ListenAndServe(fmt.Sprintf(":80"), nil))
}
