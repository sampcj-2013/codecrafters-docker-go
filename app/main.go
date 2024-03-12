package main

import (
	"fmt"
	// Uncomment this block to pass the first stage!
	"os"
	"os/exec"
	// "log"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err := cmd.Run()
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}
}
