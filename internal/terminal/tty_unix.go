//go:build darwin || linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Run 将本地终端原样连接远端PTY；Ctrl-C是远端输入，退出时始终恢复本地终端。
func Run(ctx context.Context, c Config, in *os.File, out, diagnostic io.Writer) (int, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return 255, errors.New("TTY_REQUIRED: SSH交互登录需要真实终端")
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return 255, errors.New("TTY_SIZE_FAILED: 无法读取终端尺寸")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return 255, errors.New("TTY_RAW_FAILED: 无法设置终端模式")
	}
	defer term.Restore(fd, state)
	s, err := connect(ctx, c, out, diagnostic, rows, cols)
	if err != nil {
		return 255, err
	}
	defer s.close()
	active, cancel := context.WithCancel(ctx)
	defer cancel()
	windows := make(chan os.Signal, 1)
	signal.Notify(windows, syscall.SIGWINCH)
	defer signal.Stop(windows)
	inputDone := make(chan struct{})
	go func() { defer close(inputDone); copyInput(active, fd, s.input) }()
	resizeDone := make(chan struct{})
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-active.Done():
				return
			case <-windows:
				if w, h, e := term.GetSize(fd); e == nil {
					_ = s.remote.WindowChange(h, w)
				}
			}
		}
	}()
	code, err := s.wait()
	cancel()
	s.close()
	<-inputDone
	<-resizeDone
	return code, err
}

func copyInput(ctx context.Context, fd int, dst io.WriteCloser) {
	defer dst.Close()
	buf := make([]byte, 32768)
	for ctx.Err() == nil {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		count, err := unix.Read(fd, buf)
		if count > 0 {
			if _, e := dst.Write(buf[:count]); e != nil {
				return
			}
		}
		if err != nil || count == 0 {
			return
		}
	}
}
