package terminal

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// 自动TOFU只固定首次观察到的key；不声称已独立验证主机身份。
func hostKeyCallback(base, address string, diagnostic io.Writer) (ssh.HostKeyCallback, error) {
	if base == "" {
		return nil, errors.New("DATA_DIR_REQUIRED: 缺少本地存储目录")
	}
	dir := filepath.Join(base, "simple", "known_hosts")
	for _, p := range []string{base, filepath.Join(base, "simple"), dir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return nil, errors.New("HOST_KEY_IO: 无法创建主机密钥目录")
		}
		st, err := os.Lstat(p)
		if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
			return nil, errors.New("UNSAFE_HOST_KEY_PATH: 主机密钥目录必须私有且不能为符号链接")
		}
	}
	name := fmt.Sprintf("%x", sha256.Sum256([]byte(address)))
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return errors.New("HOST_KEY_IO: 无法访问主机密钥目录")
		}
		defer root.Close()
		check := func() error {
			st, e := root.Lstat(name)
			if e != nil {
				return e
			}
			if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 65536 {
				return errors.New("UNSAFE_HOST_KEY_PATH: 主机密钥文件不安全")
			}
			f, e := root.Open(name)
			if e != nil {
				return errors.New("HOST_KEY_IO: 无法读取已固定主机密钥")
			}
			defer f.Close()
			data, e := io.ReadAll(io.LimitReader(f, 65537))
			if e != nil {
				return errors.New("HOST_KEY_IO: 无法读取已固定主机密钥")
			}
			stored, _, _, rest, e := ssh.ParseAuthorizedKey(data)
			if e != nil || len(bytes.TrimSpace(rest)) != 0 {
				return errors.New("HOST_KEY_INVALID: 已固定主机密钥损坏")
			}
			if !bytes.Equal(stored.Marshal(), key.Marshal()) {
				return errors.New("HOST_KEY_CHANGED: 主机密钥已变化，连接终止；请独立核验，不能自动覆盖")
			}
			return nil
		}
		if err = check(); !os.IsNotExist(err) {
			return err
		}
		// 同目录完整写入后用硬链接发布，两个首次连接也不能互相覆盖。
		f, err := os.CreateTemp(dir, ".pin-*")
		if err != nil {
			return errors.New("HOST_KEY_IO: 无法保存首次主机密钥")
		}
		defer os.Remove(f.Name())
		if err = f.Chmod(0600); err == nil {
			_, err = f.Write(ssh.MarshalAuthorizedKey(key))
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = root.Link(filepath.Base(f.Name()), name)
		}
		if os.IsExist(err) {
			return check()
		}
		if err != nil {
			return errors.New("HOST_KEY_IO: 无法保存首次主机密钥")
		}
		_, _ = fmt.Fprintf(diagnostic, "TOFU: 已固定首次主机密钥 %s %s（未经独立身份核验）\r\n", address, ssh.FingerprintSHA256(key))
		return nil
	}, nil
}
