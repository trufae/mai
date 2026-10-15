//go:build !darwin

package main

import (
	"context"
	"fmt"
)

func lockAutomation(context.Context) (func(), error) {
	return nil, fmt.Errorf("Mail automation requires macOS")
}
