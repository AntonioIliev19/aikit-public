//go:build !cgo

package parser

import "context"

func Available() bool                                             { return false }
func parseNative(context.Context, string, []byte) (Result, error) { return Result{}, ErrUnavailable }
