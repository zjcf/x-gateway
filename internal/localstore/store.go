package localstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"x-gateway/internal/model"
)

const embeddedSeed = "x-gateway/simple/v2/local-machine-key-domain"
const maxFile = 16 << 20

type Store struct{ dir string }
type sealed struct {
	KeyID string `json:"key_id"`
	Nonce []byte `json:"nonce"`
	Data  []byte `json:"ciphertext"`
}
type keyring struct {
	ID  string `json:"id"`
	Key []byte `json:"key"`
}
type document struct {
	Version int               `json:"version"`
	Keyring sealed            `json:"keyring"`
	Records map[string]sealed `json:"records"`
}
type loaded struct {
	doc     document
	ring    keyring
	master  []byte
	records map[string]Record
}

func failure(code string) error    { return model.Err(code, "本地凭据存储不可用") }
func random(n int) ([]byte, error) { b := make([]byte, n); _, e := rand.Read(b); return b, e }
func seal(key []byte, id, keyID string, p []byte) (sealed, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return sealed{}, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return sealed{}, e
	}
	nonce, e := random(12)
	if e != nil {
		return sealed{}, e
	}
	return sealed{keyID, nonce, g.Seal(nil, nonce, p, []byte("x-gateway/simple/v2:"+id+":"+keyID))}, nil
}
func unseal(key []byte, id string, s sealed) ([]byte, error) {
	if len(s.Nonce) != 12 {
		return nil, failure("STORE_CORRUPT")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, failure("STORE_CORRUPT")
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, failure("STORE_CORRUPT")
	}
	p, e := g.Open(nil, s.Nonce, s.Data, []byte("x-gateway/simple/v2:"+id+":"+s.KeyID))
	if e != nil {
		return nil, failure("STORE_CORRUPT")
	}
	return p, nil
}
func recordID(kind, target string, user ...string) string {
	b := []byte(kind + "\x00" + target)
	if kind == "web" && len(user) == 1 {
		b, _ = json.Marshal([]string{kind, target, user[0]})
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func privateDir(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return failure("STORE_IO")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return model.Err("UNSAFE_PERMISSIONS", "数据目录必须为0700且不能是符号链接")
	}
	return nil
}
func Open(baseDir string) (*Store, error) {
	if baseDir == "" {
		baseDir = DefaultDir()
	}
	if baseDir == "" {
		return nil, failure("STORE_IO")
	}
	abs, e := filepath.Abs(baseDir)
	if e != nil {
		return nil, failure("STORE_IO")
	}
	if e = privateDir(abs); e != nil {
		return nil, e
	}
	s := &Store{dir: filepath.Join(abs, "simple")}
	if e = privateDir(s.dir); e != nil {
		return nil, e
	}
	if e = s.withLocked(func(*loaded) error { return nil }); e != nil {
		return nil, e
	}
	return s, nil
}
func readPrivate(path string, limit int) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, failure("UNSAFE_PERMISSIONS")
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, failure("STORE_IO")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) > limit {
		return nil, failure("STORE_CORRUPT")
	}
	return b, nil
}
func (s *Store) withLocked(fn func(*loaded) error) error {
	fd, e := unix.Open(filepath.Join(s.dir, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return failure("STORE_IO")
	}
	f := os.NewFile(uintptr(fd), "credential-lock")
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return failure("UNSAFE_PERMISSIONS")
	}
	if e = unix.Flock(fd, unix.LOCK_EX); e != nil {
		return failure("STORE_IO")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	state, e := s.load()
	if e != nil {
		return e
	}
	defer clear(state.master)
	defer clear(state.ring.Key)
	return fn(state)
}
func (s *Store) load() (*loaded, error) {
	path := filepath.Join(s.dir, "credentials.json")
	b, e := readPrivate(path, maxFile)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	machinePath := filepath.Join(s.dir, "machine.key")
	machine, e := readPrivate(machinePath, 32)
	if os.IsNotExist(e) {
		if exists {
			return nil, model.Err("MACHINE_KEY_MISSING", "已有密文但machine.key丢失；请恢复备份，不能自动生成新key")
		}
		machine, e = random(32)
		if e != nil {
			return nil, failure("STORE_IO")
		}
		if e = atomicWrite(machinePath, machine); e != nil {
			clear(machine)
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	defer clear(machine)
	if len(machine) != 32 {
		return nil, failure("MACHINE_KEY_INVALID")
	}
	mac := hmac.New(sha256.New, []byte(embeddedSeed))
	mac.Write(machine)
	state := &loaded{master: mac.Sum(nil), records: map[string]Record{}}
	ok := false
	defer func() {
		if !ok {
			clear(state.master)
			clear(state.ring.Key)
		}
	}()
	if !exists {
		key, e := random(32)
		if e != nil {
			return nil, failure("STORE_IO")
		}
		id, e := random(12)
		if e != nil {
			clear(key)
			return nil, failure("STORE_IO")
		}
		state.ring = keyring{hex.EncodeToString(id), key}
		state.doc = document{Version: 2, Records: map[string]sealed{}}
		if e = s.save(state); e != nil {
			return nil, e
		}
		ok = true
		return state, nil
	}
	if json.Unmarshal(b, &state.doc) != nil || state.doc.Version != 2 || state.doc.Records == nil || state.doc.Keyring.KeyID != "machine" {
		return nil, failure("STORE_CORRUPT")
	}
	plain, e := unseal(state.master, "keyring", state.doc.Keyring)
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(plain, &state.ring)
	clear(plain)
	if e != nil || len(state.ring.Key) != 32 || state.ring.ID == "" {
		return nil, failure("STORE_CORRUPT")
	}
	for id, value := range state.doc.Records {
		if value.KeyID != state.ring.ID {
			return nil, failure("STORE_CORRUPT")
		}
		plain, e := unseal(state.ring.Key, id, value)
		if e != nil {
			return nil, e
		}
		var r Record
		e = json.Unmarshal(plain, &r)
		clear(plain)
		if e != nil || r.ID != id || (recordID(r.Kind, r.Target) != id && recordID(r.Kind, r.Target, r.Username) != id) {
			return nil, failure("STORE_CORRUPT")
		}
		r, e = normalizeRecord(r)
		if e != nil {
			return nil, failure("STORE_CORRUPT")
		}
		if _, exists := state.records[r.ID]; exists {
			return nil, failure("STORE_CORRUPT")
		}
		state.records[r.ID] = r
	}
	ok = true
	return state, nil
}
func (s *Store) save(state *loaded) error {
	// Rebuild every envelope so legacy IDs and their AAD change together in one commit.
	state.doc.Records = make(map[string]sealed, len(state.records))
	for id, r := range state.records {
		plain, e := json.Marshal(r)
		if e != nil {
			return failure("STORE_IO")
		}
		entry, e := seal(state.ring.Key, id, state.ring.ID, plain)
		clear(plain)
		if e != nil {
			return failure("STORE_IO")
		}
		state.doc.Records[id] = entry
	}
	plain, e := json.Marshal(state.ring)
	if e != nil {
		return failure("STORE_IO")
	}
	defer clear(plain)
	state.doc.Keyring, e = seal(state.master, "keyring", "machine", plain)
	if e != nil {
		return failure("STORE_IO")
	}
	b, e := json.Marshal(state.doc)
	if e != nil || len(b) > maxFile {
		return failure("STORE_IO")
	}
	return atomicWrite(filepath.Join(s.dir, "credentials.json"), b)
}
func normalizeRecord(r Record) (Record, error) {
	switch r.Kind {
	case "ssh":
		address, e := ParseSSH(r.Target, r.Port)
		if e != nil {
			return r, e
		}
		user := address.Username
		if r.Username != "" && r.Username != user {
			return r, model.Err("INVALID_CREDENTIAL", "用户名必须与SSH目标一致")
		}
		if len(r.PrivateKey) > 128<<10 {
			return r, model.Err("PRIVATE_KEY_INVALID", "SSH私钥不能超过128KiB")
		}
		if r.PrivateKey != "" {
			var err error
			if r.Passphrase != "" {
				_, err = ssh.ParsePrivateKeyWithPassphrase([]byte(r.PrivateKey), []byte(r.Passphrase))
			} else {
				_, err = ssh.ParsePrivateKey([]byte(r.PrivateKey))
			}
			if err != nil {
				return r, model.Err("PRIVATE_KEY_INVALID", "SSH私钥格式或解密口令无效")
			}
		}
		if r.Password == "" && r.PrivateKey == "" {
			return r, model.Err("INVALID_CREDENTIAL", "SSH需要密码或私钥")
		}
		r.Target = address.Target()
		r.Host = address.Host
		// Legacy :22 did not distinguish an omitted port from an explicit 22.
		if r.Port == 0 && address.Port != 22 {
			r.Port = address.Port
		}
		r.Username = user
		r.Name = ""
		r.URL = ""
		r.Origin = ""
	case "web":
		if r.Target == "" || len(r.Target) > 256 || strings.ContainsAny(r.Target, "\x00\r\n") {
			return r, model.Err("INVALID_TARGET", "网站名称无效")
		}
		origin, e := WebOrigin(r.URL)
		if e != nil {
			return r, e
		}
		if r.Username == "" || r.Password == "" || strings.ContainsAny(r.Username, "\x00\r\n") {
			return r, model.Err("INVALID_CREDENTIAL", "网站用户名和密码不能为空")
		}
		if r.PrivateKey != "" || r.Passphrase != "" || r.EnablePassword != "" || r.SudoPassword != "" {
			return r, model.Err("INVALID_CREDENTIAL", "网站凭据不接受SSH秘密字段")
		}
		r.Name = r.Target
		r.Origin = origin
		r.Host = ""
		r.Port = 0
	default:
		return r, model.Err("INVALID_CREDENTIAL", "凭据类型必须为ssh或web")
	}
	r.ID = recordID(r.Kind, r.Target, r.Username)
	return r, nil
}
func (s *Store) Put(record Record) error {
	r, e := normalizeRecord(record)
	if e != nil {
		return e
	}
	return s.withLocked(func(state *loaded) error {
		if e := checkOrigin(state.records, r); e != nil {
			return e
		}
		state.records[r.ID] = r
		return s.save(state)
	})
}

func checkOrigin(records map[string]Record, r Record) error {
	if r.Kind == "web" {
		for _, other := range records {
			if other.Kind == "web" && other.Target == r.Target && other.Username != r.Username && other.Origin != r.Origin {
				return model.Err("CREDENTIAL_ORIGIN_CONFLICT", "同名网站的账号必须绑定同一origin")
			}
		}
	}
	return nil
}
func selectWeb(state *loaded, target string, user []string) (Record, error) {
	if len(user) > 1 || len(user) == 1 && user[0] == "" {
		return Record{}, model.Err("INVALID_ARGUMENT", "用户选择必须为一个非空用户名")
	}
	matches := []Record{}
	for _, r := range state.records {
		if r.Kind == "web" && r.Target == target && (len(user) == 0 || r.Username == user[0]) {
			matches = append(matches, r)
		}
	}
	if len(matches) > 1 {
		return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该网站有多个账号 "+usernames(matches)+"；请使用 --user USER 指定账号")
	}
	return unique(matches)
}
func usernames(records []Record) string {
	set := map[string]bool{}
	for _, r := range records {
		set[r.Username] = true
	}
	users := make([]string, 0, len(set))
	for user := range set {
		users = append(users, user)
	}
	sort.Strings(users)
	b, _ := json.Marshal(users)
	return string(b)
}
func unique(matches []Record) (Record, error) {
	if len(matches) == 0 {
		return Record{}, model.Err("CREDENTIAL_NOT_FOUND", "尚未保存该目标账号凭据")
	}
	if len(matches) > 1 {
		return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该目标存在多条凭据，请明确指定用户或SSH端口")
	}
	return matches[0], nil
}
func selectSSH(state *loaded, target string, port int, allowBare bool) (Record, error) {
	address, e := ParseSSH(target, port)
	if allowBare {
		address, e = ParseSSHConnection(target, port)
	}
	if e != nil {
		return Record{}, e
	}
	matches := []Record{}
	for _, r := range state.records {
		if r.Kind != "ssh" {
			continue
		}
		a, e := ParseSSH(r.Target, 0)
		if e != nil {
			return Record{}, failure("STORE_CORRUPT")
		}
		if (address.Username == "" || a.Username == address.Username) && a.Host == address.Host && (!address.ExplicitPort || a.Port == address.Port) {
			matches = append(matches, r)
		}
	}
	if len(matches) > 1 {
		users := map[string]bool{}
		ports := []int{}
		for _, r := range matches {
			users[r.Username] = true
			a, _ := ParseSSH(r.Target, 0)
			ports = append(ports, a.Port)
		}
		if len(users) > 1 {
			return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该SSH目标有多个账号 "+usernames(matches)+"；请使用 user@host 指定账号")
		}
		sort.Ints(ports)
		b, _ := json.Marshal(ports)
		return Record{}, model.Err("CREDENTIAL_AMBIGUOUS", "该SSH账号有多个端口 "+string(b)+"；请使用 -p PORT 指定端口")
	}
	return unique(matches)
}
func (s *Store) Get(kind, target string, user ...string) (Record, error) {
	var out Record
	e := s.withLocked(func(state *loaded) error {
		var e error
		switch kind {
		case "web":
			out, e = selectWeb(state, target, user)
		case "ssh":
			if len(user) != 0 {
				return model.Err("INVALID_ARGUMENT", "SSH用户名来自目标地址")
			}
			out, e = selectSSH(state, target, 0, false)
		default:
			e = model.Err("INVALID_CREDENTIAL", "未知凭据类型")
		}
		return e
	})
	return out, e
}
func (s *Store) GetSSH(target string, port int) (Record, error) {
	var out Record
	e := s.withLocked(func(state *loaded) error { var e error; out, e = selectSSH(state, target, port, true); return e })
	return out, e
}
func (s *Store) List(kind string) ([]Metadata, error) {
	out := []Metadata{}
	e := s.withLocked(func(state *loaded) error {
		for _, r := range state.records {
			if kind == "" || r.Kind == kind {
				out = append(out, Metadata{ID: r.ID, Kind: r.Kind, Target: r.Target, Name: r.Name, Origin: r.Origin, Username: r.Username, Host: r.Host, Port: r.Port})
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Username < out[j].Username
	})
	return out, e
}
func (s *Store) DeleteSSH(target string, port int) error {
	return s.withLocked(func(state *loaded) error {
		r, e := selectSSH(state, target, port, false)
		if e != nil {
			return e
		}
		delete(state.records, r.ID)
		return s.save(state)
	})
}

// DeleteWeb captures the selected account's metadata and deletes it under one lock.
func (s *Store) DeleteWeb(target string, user ...string) (Metadata, error) {
	var out Metadata
	e := s.withLocked(func(state *loaded) error {
		r, e := selectWeb(state, target, user)
		if e != nil {
			return e
		}
		delete(state.records, r.ID)
		if e = s.save(state); e != nil {
			return e
		}
		out = Metadata{ID: r.ID, Kind: r.Kind, Target: r.Target, Name: r.Name, Origin: r.Origin, Username: r.Username}
		return nil
	})
	return out, e
}
func (s *Store) RotateKey() error {
	return s.withLocked(func(state *loaded) error {
		key, e := random(32)
		if e != nil {
			return failure("STORE_IO")
		}
		defer clear(key)
		id, e := random(12)
		if e != nil {
			return failure("STORE_IO")
		}
		old := state.ring.Key
		state.ring = keyring{hex.EncodeToString(id), key}
		defer clear(old)
		return s.save(state)
	})
}
func atomicWrite(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if e != nil {
		return failure("STORE_IO")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return failure("STORE_IO")
	}
	if _, e = f.Write(b); e != nil {
		return failure("STORE_IO")
	}
	if e = f.Sync(); e != nil {
		return failure("STORE_IO")
	}
	if e = f.Close(); e != nil {
		return failure("STORE_IO")
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return failure("STORE_IO")
	}
	if dir, e := os.Open(filepath.Dir(path)); e == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}
