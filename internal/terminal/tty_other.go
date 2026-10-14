//go:build !darwin && !linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os"
)

func Run(context.Context, Config, *os.File, io.Writer, io.Writer) (int, error) {
	return 255, errors.New("UNSUPPORTED: 当前平台不支持交互终端")
}
