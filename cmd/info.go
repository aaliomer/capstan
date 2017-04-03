/*
 * Copyright (C) 2014 Cloudius Systems, Ltd.
 *
 * This work is open source software, licensed under the terms of the
 * BSD license as described in the LICENSE file in the top-level directory.
 */

package cmd

import (
	"fmt"
	"github.com/aaliomer/capstan/image"
)

func Info(path string) error {
	format, err := image.Probe(path)
	if err != nil {
		return err
	}
	switch format {
	case image.VDI:
		fmt.Printf("%s: VDI\n", path)
	case image.QCOW2:
		fmt.Printf("%s: QCOW2\n", path)
	case image.VMDK:
		fmt.Printf("%s: VMDK\n", path)
	default:
		fmt.Printf("%s: not a runnable image\n", path)
	}
	return nil
}
