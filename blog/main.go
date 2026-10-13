package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"
)

//go:embed index.html
var indexHTML embed.FS

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := indexHTML.ReadFile("index.html")
		if err != nil {
			http.Error(w, "Could not read index.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	http.HandleFunc("/clicked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<div><b>Thanks for clicking! This was loaded via HTMX.</b></div>")
	})

	fmt.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
