// Command serve serves the web directory for the browser host.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "localhost:8092", "listen address")
	dir := flag.String("dir", "web", "directory to serve")
	flag.Parse()
	log.Printf("serving %s on http://%s", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, http.FileServer(http.Dir(*dir))))
}
