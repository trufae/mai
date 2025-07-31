//go:build linux
// +build linux

package term

import "golang.org/x/sys/unix"

const ioctlGetTermios = unix.TCGETS
const ioctlSetTermios = unix.TCSETS
