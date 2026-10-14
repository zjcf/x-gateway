package localstore

import (
	"os"
	"path/filepath"
)

// Record is internal only. Public output must use Metadata instead.
type Record struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url,omitempty"`
	Origin         string `json:"origin,omitempty"`
	Username       string `json:"username"`
	Host           string `json:"host,omitempty"`
	Port           int    `json:"port,omitempty"`
	Password       string `json:"password,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"`
	Passphrase     string `json:"passphrase,omitempty"`
	EnablePassword string `json:"enable_password,omitempty"`
	SudoPassword   string `json:"sudo_password,omitempty"`
}

type Metadata struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Name     string `json:"name,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Username string `json:"username"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
}

func DefaultDir() string {
	dir, e := os.UserConfigDir()
	if e != nil {
		return ""
	}
	return filepath.Join(dir, "x-gateway")
}
