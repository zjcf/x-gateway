// Package terminal 提供直接 SSH 终端，不经过常驻服务或输出转换。
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host       string
	Port       int
	Username   string
	Password   string
	PrivateKey string
	Passphrase string
	DataDir    string
	Term       string
}

type session struct {
	client *ssh.Client
	remote *ssh.Session
	input  io.WriteCloser
	stop   func() bool
}

func connect(ctx context.Context, c Config, out, diagnostic io.Writer, rows, cols int) (*session, error) {
	if c.Host == "" || c.Username == "" || c.Port < 1 || c.Port > 65535 {
		return nil, errors.New("INVALID_ARGUMENT: SSH地址、账户或端口无效")
	}
	auth := []ssh.AuthMethod{}
	if c.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			return nil, errors.New("INVALID_PRIVATE_KEY: 无法读取SSH私钥")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("CREDENTIAL_REQUIRED: 请先保存SSH密码或私钥")
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	verify, err := hostKeyCallback(c.DataDir, address, diagnostic)
	if err != nil {
		return nil, err
	}
	setup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(setup, "tcp", address)
	if err != nil {
		return nil, errors.New("SSH_CONNECT_FAILED: 无法连接SSH主机")
	}
	deadline, _ := setup.Deadline()
	_ = conn.SetDeadline(deadline)
	stopSetup := context.AfterFunc(setup, func() { _ = conn.Close() })
	defer stopSetup()
	var keyErr error
	config := &ssh.ClientConfig{User: c.Username, Auth: auth, HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
		keyErr = verify(host, remote, key)
		return keyErr
	}}
	cc, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		if keyErr != nil {
			return nil, keyErr
		}
		if setup.Err() != nil || !time.Now().Before(deadline) {
			return nil, errors.New("SSH_TIMEOUT: SSH握手已取消或超时")
		}
		return nil, errors.New("SSH_AUTH_OR_HANDSHAKE_FAILED: SSH认证或握手失败")
	}
	s := &session{client: ssh.NewClient(cc, channels, requests)}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	s.remote, err = s.client.NewSession()
	if err != nil {
		return nil, errors.New("SSH_SESSION_FAILED: 无法创建SSH终端")
	}
	s.remote.Stdout, s.remote.Stderr = out, diagnostic
	s.input, err = s.remote.StdinPipe()
	if err != nil {
		return nil, errors.New("SSH_SESSION_FAILED: 无法创建SSH输入流")
	}
	if c.Term == "" {
		c.Term = "xterm-256color"
	}
	if err = s.remote.RequestPty(c.Term, rows, cols, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}); err != nil {
		return nil, errors.New("SSH_PTY_FAILED: 远端拒绝PTY")
	}
	if err = s.remote.Shell(); err != nil {
		return nil, errors.New("SSH_SHELL_FAILED: 远端拒绝交互Shell")
	}
	// setup取消不能关闭成功建立的连接；活动会话由原始ctx管理。
	stopSetup()
	if setup.Err() != nil {
		return nil, errors.New("SSH_TIMEOUT: SSH终端启动已取消或超时")
	}
	_ = conn.SetDeadline(time.Time{})
	s.stop = context.AfterFunc(ctx, func() { _ = s.client.Close() })
	ok = true
	return s, nil
}

func (s *session) close() {
	if s.stop != nil {
		s.stop()
	}
	if s.remote != nil {
		_ = s.remote.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
}

func (s *session) wait() (int, error) {
	err := s.remote.Wait()
	if err == nil {
		return 0, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus(), nil
	}
	return 255, fmt.Errorf("SSH_DISCONNECTED: SSH连接中断，未收到退出状态")
}
