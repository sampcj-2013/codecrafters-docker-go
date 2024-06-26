package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
)

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
		fmt.Printf("Directory: %s\n", f.Name())
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

func copyFile(sourcePath, currentPath, destinationPath, fileToCopy string) error {
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}

	fs, err := file.Stat()
	if err != nil {
		return err
	}
	permissions := fs.Mode().Perm()

	newFilePath := fmt.Sprintf("%s%s", currentPath, destinationPath)
	if len(debugCapabilities) > 0 {
		fmt.Printf("Copying to new file: %s\n", newFilePath)
	}

	err = os.MkdirAll(newFilePath, 0750)
	if err != nil {
		return err
	}

	destinationFile, err := os.Create(fmt.Sprintf("%s%s", newFilePath, fileToCopy))
	if err != nil {
		return err
	}

	if err = destinationFile.Chmod(permissions); err != nil {
		return err
	}

	buf := make([]byte, fs.Size()+1)

	defer destinationFile.Close()
	defer file.Close()

	for {
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			return nil
		}

		if _, err := destinationFile.Write(buf[:n]); err != nil {
			return err
		}
	}
}

func setup_chroot(path string) error {
	if len(debugCapabilities) > 0 {
		fmt.Printf("Temporary directory for chroot: %s\n", path)
	}
	// Ideally use syscall.PivotRoot here
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

func untar(dst string, r io.Reader) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case header == nil:
			continue
		}
		target := filepath.Join(dst, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
			}
		case tar.TypeSymlink:
			os.Symlink(header.Linkname, filepath.Join(dst,header.Name))
			if err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			f.Close()
		}
	}
}
