package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

func makeReadme(filename string) {
	date := time.Now().Format("2 Jan 2006 15:04 +0100 UTC")

	// Whisk together static and dynamic content until stiff peaks form
	hello := "### Hello! I'm Patrick.\n\n" +
		":v: Go enthusiast\n\n:muscle: DevOps lover\n\n" +
		"Currently I am building my Jenkins pipeline with some GitHub Actions & Workflows to have a full ci/cd pipeline up and running for my golang application.\nThe Golang Application is not ready yet and currently I am only getting the tests running.\n\nThe other project I am working on is a go application, that catches inputs from the terminal and saves it to a file. The hard part (for me at least) is to safe new inputs into a new line automaticly\n\n\n![Image alt text](/images/gopher_with_coffee.gif)"
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
