//go:build !linux
// +build !linux

package main

import "golang.org/x/sys/unix"

const ioctlGetTermios = unix.TIOCGETA
const ioctlSetTermios = unix.TIOCSETA
