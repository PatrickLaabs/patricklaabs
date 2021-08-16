package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

func makeReadme(filename string) {
	date := time.Now().Format("2 Jan 2006")

	// Whisk together static and dynamic content until stiff peaks form
	hello := "### Hello! I'm Patrick.\n\n" +
		":v: Go enthusiast\n\n:muscle: DevOps lover\n\n" +
		"---\n\n" +
		"I build a pipeline for/in GitHub Actions for golang projects.\n The Goal is to have a clean and easy to set up pipeline" +
		"for almost every golang project\nso no more rebuilding the pipe for every project.\n\n" +
		"Well, lets see if this works out as intended. At least its empowered by goreleaser :heart:\n" +
		"[https://github.com/PatrickLaabs/golang-pipeline](https://github.com/PatrickLaabs/golang-pipeline)\n\n" +
		"[Image alt text](/images/gopher_with_coffee.gif)"
	updated := "<sub>Lastest update on " + date + ".</sub>"
	data := fmt.Sprintf("%s\n\n\n%s\n", hello, updated)

	// Prepare file with a light coating of os
	file, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Bake at n bytes per second until golden brown
	_, err = io.WriteString(file, data)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	makeReadme("../README.md")
}
