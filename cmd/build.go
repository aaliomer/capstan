/*
 * Copyright (C) 2014 Cloudius Systems, Ltd.
 *
 * This work is open source software, licensed under the terms of the
 * BSD license as described in the LICENSE file in the top-level directory.
 */

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/cheggaaa/pb"
	"github.com/aaliomer/capstan/core"
	"github.com/aaliomer/capstan/cpio"
	"github.com/aaliomer/capstan/hypervisor/qemu"
	"github.com/aaliomer/capstan/nat"
	"github.com/aaliomer/capstan/nbd"
	"github.com/aaliomer/capstan/util"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Build(r *util.Repo, image *core.Image, template *core.Template, verbose bool, mem string) error {
	//create the image directory
	fmt.Println("Custom Build by Jay.. shout out to optum")
	if err := os.MkdirAll(filepath.Dir(r.ImagePath(image.Hypervisor, image.Name)), 0777); err != nil {
		return err
	}
	fmt.Printf("Building %s in directory %s (pwd %s)...\n", image.Name, filepath.Dir(r.ImagePath(image.Hypervisor, image.Name), filepath.Abs(filepath.Dir(os.Args[0])))
	//if it has a build script
	if template.Build != "" {
		args := strings.Fields(template.Build)
		cmd := exec.Command(args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Error executing the build command "+ args[0] + " : " + string(out))
			return err
		}
	}
	//save the file as <img name>.<hypervisor name> in the repo
	fmt.Println("Checking for image locally", template, r, image.Hypervisor)
	if err := checkConfig(template, r, image.Hypervisor); err != nil {
		return err
	}
	if template.RpmBase != nil {
		template.RpmBase.Download()
	}
	//we copy the base image to the repository directory
	fmt.Println("Copying " + r.ImagePath(image.Hypervisor, template.Base))
	cmd := util.CopyFile(r.ImagePath(image.Hypervisor, template.Base), r.ImagePath(image.Hypervisor, image.Name))
	_, err := cmd.Output()
	if err != nil {
		return err
	}

	//cpiod is an archive tool
	cmdline := "/tools/cpiod.so"
	if verbose {
		cmdline = "--verbose" + cmdline
	}
	//TODO DIG
	//use qemu-nbd for network block device share
	if err := SetArgs(r, image.Hypervisor, image.Name, "/tools/cpiod.so"); err != nil {
		return err
	}
	if template.RpmBase != nil {
		if err := UploadRPM(r, image.Hypervisor, image.Name, template, verbose, mem); err != nil {
			return err
		}
	}
	if err := UploadFiles(r, image.Hypervisor, image.Name, template, verbose, mem); err != nil {
		return err
	}
	//TODO
	//HINT: command line is the one executing in the capstan image
	return SetArgs(r, image.Hypervisor, image.Name, template.Cmdline)
}

func checkConfig(t *core.Template, r *util.Repo, hypervisor string) error {
	if _, err := os.Stat(r.ImagePath(hypervisor, t.Base)); os.IsNotExist(err) {
		if err := Pull(r, hypervisor, t.Base); err != nil {
			return err
		}
	}
	for _, value := range t.Files {
		if _, err := os.Stat(value); os.IsNotExist(err) {
			return errors.New(fmt.Sprintf("%s: no such file or directory", value))
		}
	}
	return nil
}

func UploadRPM(r *util.Repo, hypervisor string, image string, template *core.Template, verbose bool, mem string) error {
	file := r.ImagePath(hypervisor, image)
	size, err := util.ParseMemSize(mem)
	if err != nil {
		return err
	}
	vmconfig := &qemu.VMConfig{
		Image:       file,
		Verbose:     verbose,
		Memory:      size,
		Networking:  "nat",
		NatRules:    []nat.Rule{nat.Rule{GuestPort: "10000", HostPort: "10000"}},
		BackingFile: false,
	}
	vm, err := qemu.LaunchVM(vmconfig)
	if err != nil {
		return err
	}
	defer vm.Process.Kill()

	conn, err := util.ConnectAndWait("tcp", "localhost:10000")
	if err != nil {
		return err
	}

	cmd := exec.Command("rpm2cpio", template.RpmBase.Filename())
	cmd.Stdout = conn
	err = cmd.Start()
	if err != nil {
		return err
	}
	defer cmd.Wait()

	err = vm.Wait()

	conn.Close()

	return err
}

func IsReg(m os.FileMode) bool {
	nonreg := os.ModeDir | os.ModeSymlink | os.ModeDevice | os.ModeSocket | os.ModeCharDevice
	return (m & nonreg) == 0
}

func copyFile(conn net.Conn, src string, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}

	if fi.IsDir() {
		fi, err := os.Stat(src)
		if err != nil {
			return err
		}
		perm := uint64(fi.Mode()) & 0777
		cpio.WritePadded(conn, cpio.ToWireFormat(dst, cpio.C_ISDIR|perm, 0))
		return nil
	}

	if !IsReg(fi.Mode()) {
		fmt.Println("skipping non-file path " + src)
		return nil
	} else {
		contents, err := ioutil.ReadFile(src)
		if err != nil {
			return nil
		}
		fi, err := os.Stat(src)
		if err != nil {
			return err
		}
		perm := uint64(fi.Mode()) & 0777
		cpio.WritePadded(conn, cpio.ToWireFormat(dst, cpio.C_ISREG|perm, fi.Size()))
		cpio.WritePadded(conn, contents)
	}

	return nil
}

func UploadFiles(r *util.Repo, hypervisor string, image string, t *core.Template, verbose bool, mem string) error {
	fmt.Println("UploadFiles with params ","util.Repo: ", r , t, mem )
	file := r.ImagePath(hypervisor, image)
	size, err := util.ParseMemSize(mem)
	if err != nil {
		return err
	}
	vmconfig := &qemu.VMConfig{
		Image:       file,
		Verbose:     verbose,
		Memory:      size,
		Networking:  "nat",
		NatRules:    []nat.Rule{nat.Rule{GuestPort: "10000", HostPort: "10000"}},
		BackingFile: false,
	}
	cmd, err := qemu.VMCommand(vmconfig)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		text := scanner.Text()
		if verbose {
			fmt.Println(text)
		}
		if text == "Waiting for connection from host..." {
			break
		}
	}
	if verbose {
		go io.Copy(os.Stdout, stdout)
		go io.Copy(os.Stderr, stderr)
	} else {
		go io.Copy(ioutil.Discard, stdout)
		go io.Copy(ioutil.Discard, stderr)
	}
	conn, err := util.ConnectAndWait("tcp", "localhost:10000")
	if err != nil {
		return err
	}

	rootfsFiles := make(map[string]string)
	if _, err = os.Stat(t.Rootfs); !os.IsNotExist(err) {
		err = filepath.Walk(t.Rootfs, func(src string, info os.FileInfo, _ error) error {
			dst := strings.Replace(src, t.Rootfs, "", 1)
			if dst != "" {
				rootfsFiles[dst] = src
			}
			return nil
		})
	}

	fmt.Println("setting file system map", rootfsFiles)
	fmt.Println("Uploading files...")
	bar := pb.New(len(rootfsFiles) + len(t.Files))
	//TODO REMOVE
	verbose = true
	if !verbose {
		bar.Start()
	}
	for dst, src := range rootfsFiles {
		err = copyFile(conn, src, dst)
		if verbose {
			fmt.Println(src + "  --> " + dst)
		} else {
			bar.Increment()
		}
		if err != nil {
			return err
		}
	}

	for dst, src := range t.Files {
		err = copyFile(conn, src, dst)
		if verbose {
			fmt.Println(src + "  --> " + dst)
		} else {
			bar.Increment()
		}
		if err != nil {
			return err
		}
	}

	cpio.WritePadded(conn, cpio.ToWireFormat("TRAILER!!!", 0, 0))

	conn.Close()
	return cmd.Wait()
}

func SetArgs(r *util.Repo, hypervisor, image string, args string) error {
	file := r.ImagePath(hypervisor, image)
	fmt.Println("Executing command: (SetArgs) qemu-nbd", "-p", "10809", file)
	cmd := exec.Command("qemu-nbd", "-p", "10809", file)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	err = cmd.Start()
	if err != nil {
		return err
	}
	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)
	fmt.Println("connecting to localhost:10809")
	conn, err := util.ConnectAndWait("tcp", "localhost:10809")
	if err != nil {
		fmt.Println("connection failed", err)
		return err
	}
	fmt.Println("connection successfull")

	session := &nbd.NbdSession{
		Conn:   conn,
		Handle: 0,
	}
	if err := session.Handshake(); err != nil {
		return err
	}

	padding := 512 - (len(args) % 512)

	data := append([]byte(args), make([]byte, padding)...)

	if err := session.Write(512, data); err != nil {
		return err
	}
	if err := session.Flush(); err != nil {
		return err
	}
	if err := session.Disconnect(); err != nil {
		return err
	}
	conn.Close()
	cmd.Wait()

	return nil
}
