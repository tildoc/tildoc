// Command officestub runs the stand-in for Collabora Online on an address,
// for the UI smoke test.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/tildoc/tildoc/internal/wopi/wopitest"
)

func main() {
	s, err := wopitest.New()
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(os.Args[1], s.Handler()))
}
