package main

import (
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/plain")

		w.Write([]byte("service unavailable. please check back again in a few minutes"))
	})

	http.ListenAndServe(":"+os.Getenv("PORT"), nil)
}
