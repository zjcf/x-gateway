//go:build darwin || linux

package terminal

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

type closeBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *closeBuffer) Close() error { b.closed = true; return nil }

func TestInputControlBytesAndCancellation(t *testing.T) {
	for _, cancelOnly := range []bool{false, true} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		dst := &closeBuffer{}
		done := make(chan struct{})
		go func() { defer close(done); copyInput(ctx, int(reader.Fd()), dst) }()
		want := []byte("\x03\x04\x1b[A你好\r")
		if cancelOnly {
			cancel()
		} else {
			if _, err = writer.Write(want); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("input goroutine leaked")
		}
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		if !dst.closed {
			t.Fatal("remote input not closed")
		}
		if !cancelOnly && !bytes.Equal(dst.Bytes(), want) {
			t.Fatalf("input changed: %q", dst.Bytes())
		}
	}
}

func TestRunRejectsNonTTYWithoutChangingInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	code, err := Run(context.Background(), Config{}, reader, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 255 || err == nil {
		t.Fatalf("exit=%d err=%v", code, err)
	}
}
