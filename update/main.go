package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func makeReadme(filename string) error {
	date := time.Now().Format("2 Jan 2006")

	// Whisk together static and dynamic content until stiff peaks form
	hello := "### Hello! I'm Patrick.\n\nGo enthusiast and just getting started on my journey to DevOps :) \n\nCurrently I am building my Jenkins pipeline with some GitHub Actions & Workflows to have a full ci/cd pipeline up and running for my golang application.\nThe Golang Application is not ready yet and currently I am only getting the tests running.\nNext thing that I want to have running is Goreleaser... but this may take some time :D"
	updated := "<sub>Lastest update on " + date + ".</sub>"
	data := fmt.Sprintf("%s\n\n\n%s\n", hello, updated)

	// Prepare file with a light coating of os
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Bake at n bytes per second until golden brown
	_, err = io.WriteString(file, data)
	if err != nil {
		return err
	}
	return file.Sync()
}

func main() {

	makeReadme("../README.md")

}
