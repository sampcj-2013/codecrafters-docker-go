//go:build !internalsamdebug
// +build !internalsamdebug

package main

import (
	"fmt"
	// "kernel.org/pub/linux/libs/security/libcap/cap"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"
)

// NOTE: Helpful debugging build flags for checking system capaabilities on host
// go build  -ldflags "-X main.debugCapabilities=yes"
var debugCapabilities string

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	if len(os.Args) < 4 {
		fmt.Println("Incorrect number of arguments specified.\n")
		os.Exit(1)
	}

	// We only support the "run" command for now.
	if os.Args[1] != "run" {
		fmt.Println("Only the 'run' option is currently supported")
		os.Exit(1)
	}
	ref := os.Args[2]

	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	// Pull the image down
	if err := pullImage(ref, nil); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	cmd := exec.Command(command, args...)

	// TODO: We should create a true character file here
	if cmd.Stdin == nil || cmd.Stderr == nil || cmd.Stdout == nil {
		if createFileError := os.WriteFile("/dev/null", []byte(""), 0666); createFileError != nil {
			fmt.Printf("Unable to get stdin/stdout/stderr\n")
			os.Exit(1)
		}

		// NOTE: If we are already running in a containerised environment we may not have the
		//	 capabalities available to us to create a true character device file.
		//       In that case we should do a check for capabilities here, otherwise...
		// if len(debugCapabilities) > 0 {
		// 	caps := cap.GetProc()
		// 	fmt.Printf("Available capabalities on this system: %q\n", caps)
		// }

		// if err := createCharacterfile("/tmp/mynull"); err != nil {
		// 	fmt.Printf("Error: %s\n", err)
		// 	fmt.Printf("Unable to get stdin/stdout/stderr\n")
		// 	os.Exit(1)
		// }
	}

	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	// fmt.Printf("Available capabilities: %q\n", syscall.SysProcAttr{})
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID,
	}

	// TODO: Provide a better location than /tmp
	chdir, err := ioutil.TempDir("/tmp/", "container.")
	if err != nil {
		fmt.Println("Could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(chdir)

	if len(debugCapabilities) > 0 {
		err = copyFile("./docker-explorer", chdir, "/usr/local/bin/", "docker-explorer")
		if err != nil {
			fmt.Printf("Error copying file: %s\n", err)
		}
	}

	err = copyFile("/usr/local/bin/docker-explorer", chdir, "/usr/local/bin/", "docker-explorer")
	if err != nil {
		fmt.Printf("Error copying file: %s\n", err)
		os.Exit(1)
	}

	err = setup_chroot(chdir)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if len(debugCapabilities) > 0 {
		pwd, err := cwd()
		if err != nil {
			fmt.Printf("Error getting current working directory:\n", err)
		}
		fmt.Printf("Current working directory: %s\n", pwd)

		err = lwd()
		if err != nil {
			fmt.Printf("Error getting working directory listing:\n", err)
		}
	}

	err = cmd.Run()
	if err != nil {
		fmt.Printf("Err: %v\n", err)
		if exitError, ok := err.(*exec.ExitError); ok {
			os.Exit(exitError.ExitCode())
		}
	}
}
