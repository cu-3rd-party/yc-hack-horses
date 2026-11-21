//go:build !cgo

package main

import (
	"context"
	"io"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

func exportMasks(context.Context, string, string, io.Reader, io.Writer) error {
	return inference.ErrModelUnavailable
}
