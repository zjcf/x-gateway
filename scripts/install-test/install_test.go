package installtest

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installer(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test path unavailable")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "install.sh")
}
func shell(t *testing.T, script string, args ...string) ([]byte, error) {
	t.Helper()
	argv := append([]string{"-c", script, "install-test", installer(t)}, args...)
	return exec.Command("bash", argv...).CombinedOutput()
}
func TestInstallerSyntaxHelpAndInvalidArguments(t *testing.T) {
	path := installer(t)
	tools := t.TempDir()
	marker := filepath.Join(tools, "go-called")
	os.WriteFile(filepath.Join(tools, "go"), []byte("#!/bin/sh\nprintf called > \"$XG_INSTALL_GO_CALLED\"\nexit 97\n"), 0700)
	if out, e := exec.Command("bash", "-n", path).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	var previous []byte
	for _, alias := range []string{"-h", "--help"} {
		out, e := exec.Command("bash", path, alias).CombinedOutput()
		if e != nil || !bytes.Contains(out, []byte("Go 1.25")) {
			t.Fatal("help failed")
		}
		if previous != nil && !bytes.Equal(previous, out) {
			t.Fatal("help aliases differ")
		}
		previous = out
	}
	for _, args := range [][]string{{"--extension-id", "bad"}, {"--extension-id", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}, {"--extension-id"}, {"--unknown"}, {"--extension-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--extension-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}} {
		cmd := exec.Command("bash", append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "XG_INSTALL_GO_CALLED="+marker)
		if out, e := cmd.CombinedOutput(); e == nil || !bytes.Contains(out, []byte("安装失败")) {
			t.Fatal("invalid arguments accepted")
		}
		if _, e := os.Stat(marker); !os.IsNotExist(e) {
			t.Fatal("invalid arguments reached Go before rejection")
		}
	}
}
func TestAtomicCopyAndManifestFailurePreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source file")
	destination := filepath.Join(dir, "installed file")
	os.WriteFile(source, []byte("new"), 0600)
	os.WriteFile(destination, []byte("old"), 0600)
	old, e := os.Open(destination)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	if out, e := shell(t, `source "$1"; atomic_copy "$2" "$3" 755`, source, destination); e != nil {
		t.Fatal(e, string(out))
	}
	b, _ := os.ReadFile(destination)
	stillOld, _ := io.ReadAll(old)
	info, _ := os.Stat(destination)
	if string(b) != "new" || string(stillOld) != "old" || info.Mode().Perm() != 0755 {
		t.Fatal("binary replacement was not atomic")
	}
	manifest := filepath.Join(dir, "native hosts", "manifest.json")
	os.MkdirAll(filepath.Dir(manifest), 0700)
	os.WriteFile(manifest, []byte("old-manifest"), 0600)
	binary := filepath.Join(dir, "fake xweb")
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf partial\nexit 1\n"), 0700)
	if _, e := shell(t, `source "$1"; install_manifest "$2" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa "$3"`, binary, manifest); e == nil {
		t.Fatal("failed generator accepted")
	}
	b, _ = os.ReadFile(manifest)
	if string(b) != "old-manifest" {
		t.Fatal("failed generator truncated old manifest")
	}
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf '{\"name\":\"com.x_gateway.accessctl\"}'\n"), 0700)
	if out, e := shell(t, `source "$1"; install_manifest "$2" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa "$3"`, binary, manifest); e != nil {
		t.Fatal(e, string(out))
	}
	b, _ = os.ReadFile(manifest)
	info, _ = os.Stat(manifest)
	if !bytes.Contains(b, []byte("com.x_gateway.accessctl")) || info.Mode().Perm() != 0600 {
		t.Fatal("manifest install failed")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(manifest), ".x-gateway-manifest.*"))
	if len(matches) != 0 {
		t.Fatal("manifest staging files leaked")
	}
}
func TestPathAppendIdempotentAndUnsetShell(t *testing.T) {
	rc := filepath.Join(t.TempDir(), "shell config")
	if out, e := shell(t, `source "$1"; append_path "$2"; append_path "$2"; unset SHELL; configure_path Darwin "$3"`, rc, filepath.Join(t.TempDir(), "unlisted bin")); e != nil {
		t.Fatal(e, string(out))
	}
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), `export PATH="$HOME/.local/bin:$PATH"`) != 1 {
		t.Fatal("PATH appended more than once")
	}
}
func TestMainNormalReturnCleansBuildDirectoryWithoutChangingHome(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("installer only supports macOS/Linux")
	}
	dir := t.TempDir()
	tools := filepath.Join(dir, "fake tools")
	tmp := filepath.Join(dir, "build spaces")
	os.MkdirAll(tools, 0700)
	os.MkdirAll(tmp, 0700)
	goStub := `#!/usr/bin/env bash
if [[ "$1" == env ]]; then
  case "$2" in
    GOVERSION) echo go1.25.0 ;;
    GOOS|GOHOSTOS) case "$(uname -s)" in Darwin) echo darwin ;; Linux) echo linux ;; esac ;;
    GOARCH|GOHOSTARCH) echo fixture ;;
  esac
elif [[ "$1" == build && "$2" == -o ]]; then
  printf fake > "$3"
else
  exit 1
fi
`
	os.WriteFile(filepath.Join(tools, "go"), []byte(goStub), 0700)
	// Keep the real HOME. Replace only mutation helpers so main exercises normal
	// return/EXIT cleanup while all build output remains in our temporary directory.
	script := `source "$1"
atomic_copy() { :; }
install_tree() { :; }
configure_path() { :; }
install_manifest() { :; }
main --extension-id aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`
	cmd := exec.Command("bash", "-c", script, "install-test", installer(t))
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "x-gateway-install.*"))
	if len(matches) != 0 {
		t.Fatal("main returned with build directory leaked")
	}
}

func TestChromeSessionDecision(t *testing.T) {
	for _, tc := range []struct {
		platform, display, wayland, id string
		want                           bool
	}{{"Linux", "", "", "", false}, {"Linux", ":0", "", "", true}, {"Linux", "", "wayland-0", "", true}, {"Darwin", "", "", "", true}, {"Linux", "", "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true}} {
		_, e := shell(t, `source "$1"; chrome_enabled "$2" "$3" "$4" "$5"`, tc.platform, tc.display, tc.wayland, tc.id)
		if (e == nil) != tc.want {
			t.Fatal("Chrome session decision differs from contract")
		}
	}
}
func TestInstallModesAndUniversalSkillsWithoutChangingHome(t *testing.T) {
	for _, tc := range []struct {
		name, platform, display, wayland, id string
		wantError, chrome                    bool
	}{
		{"headless", "Linux", "", "", "", false, false},
		{"x11-needs-tty", "Linux", ":0", "", "", true, true},
		{"wayland-needs-tty", "Linux", "", "wayland-0", "", true, true},
		{"mac-needs-tty", "Darwin", "", "", "", true, true},
		{"explicit-id", "Linux", "", "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := filepath.Join(t.TempDir(), "audit")
			tmp := t.TempDir()
			script := `source "$1"
test_platform="$2"; DISPLAY="$3"; WAYLAND_DISPLAY="$4"; test_id="$5"; audit="$6"
uname() { printf '%s\n' "$test_platform"; }
go() {
  if [[ "$1" == env ]]; then
    case "$2" in GOVERSION) echo go1.25.0 ;; GOOS|GOHOSTOS) echo fixture-os ;; GOARCH|GOHOSTARCH) echo fixture-arch ;; esac
  else printf fake > "$3"; fi
}
atomic_copy() { printf 'bin:%s\n' "$2" >> "$audit"; }
install_tree() { printf 'tree:%s\n' "$2" >> "$audit"; }
configure_path() { printf 'path\n' >> "$audit"; }
install_manifest() { printf 'manifest:%s\n' "$3" >> "$audit"; }
if [[ -n "$test_id" ]]; then main --extension-id "$test_id"; else main; fi
`
			cmd := exec.Command("bash", "-c", script, "install-test", installer(t), tc.platform, tc.display, tc.wayland, tc.id, audit)
			cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
			out, e := cmd.CombinedOutput()
			if (e != nil) != tc.wantError {
				t.Fatal("install mode failed", e, string(out))
			}
			b, _ := os.ReadFile(audit)
			if tc.wantError {
				if len(b) != 0 {
					t.Fatal("Chrome non-TTY rejection occurred after mutation")
				}
				return
			}
			for _, name := range []string{"accessctl-ssh", "accessctl-web"} {
				if !bytes.Contains(b, []byte("tree:"+filepath.Join(os.Getenv("HOME"), ".agents", "skills", name))) {
					t.Fatal("skills did not use universal .agents path")
				}
			}
			browserCalled := bytes.Contains(b, []byte("/x-gateway/extension")) || bytes.Contains(b, []byte("manifest:"))
			if browserCalled != tc.chrome {
				t.Fatal("headless mode touched browser helpers or explicit ID skipped them")
			}
			if !tc.chrome && (bytes.Contains(out, []byte("请粘贴")) || bytes.Contains(out, []byte("chrome://extensions"))) {
				t.Fatal("headless mode prompted for Chrome")
			}
			if !bytes.Contains(b, []byte("bin:")) || !bytes.Contains(b, []byte("path\n")) {
				t.Fatal("common install steps skipped")
			}
		})
	}
}
