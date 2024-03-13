// +build !internalsamdebug
package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	// "kernel.org/pub/linux/libs/security/libcap/cap"
	"os"
	"os/exec"
	"syscall"
	"io/ioutil"
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

	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

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
	// cmd.SysProcAttr = &syscall.SysProcAttr{
	// 	Cloneflags: syscall.CLONE_NEWUTS,
	// }

	chdir, err := ioutil.TempDir("/tmp/", "container.")
	if err != nil {
		fmt.Println("Could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(chdir)

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

func cwd() (string, error) {
	path, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return path, nil
}

func lwd() error {
	files, err := ioutil.ReadDir(".")
	if err != nil {
		return err
	}
	for _, f := range files {
		fmt.Println(f.Name())
	}
	return nil
}

func createCharacterfile(path string) error {
	// device /dev/null is set as 0x4 according to device major number
	// mode is 0x2000 for S_IFCHR on POSIX systems
	return mknod(path, 0x2000, 0x4)
}

func mknod(path string, mode uint32, dev int) error {
	return unix.Mknod(path, mode, dev)
}

func setup_chroot(path string) error {
	if len(debugCapabilities) > 0 {
		fmt.Printf("Temporary directory for chroot: %s\n", path)
	}

	err := syscall.Chroot(path)
	if err != nil {
		msg := fmt.Sprintf("Could not set chroot: %s\n", err)
		return errors.New(msg)
	}
	err = syscall.Chdir("/")
	if err != nil {
		msg := fmt.Sprintf("Could not change directory: %s\n", err)
		return errors.New(msg)
	}
	return nil
}
