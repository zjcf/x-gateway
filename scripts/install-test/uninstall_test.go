package installtest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func uninstaller(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(installer(t)), "uninstall.sh")
}
func uninstallShell(t *testing.T, script string, args ...string) ([]byte, error) {
	t.Helper()
	argv := append([]string{"-c", script, "uninstall-test", uninstaller(t)}, args...)
	return exec.Command("bash", argv...).CombinedOutput()
}
func fixture(t *testing.T) ([]string, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "bin"), filepath.Join(root, "skills"), filepath.Join(root, "share spaces", "extension"), filepath.Join(root, "Native Messaging"), filepath.Join(root, "data root")}
	files := []string{filepath.Join(paths[0], "xssh"), filepath.Join(paths[0], "xweb"), filepath.Join(paths[1], "accessctl-ssh", "SKILL.md"), filepath.Join(paths[1], "accessctl-web", "SKILL.md"), filepath.Join(paths[2], "manifest.json"), filepath.Join(paths[3], "com.x_gateway.accessctl.json"), filepath.Join(paths[4], "xweb-native-host.sh")}
	preserved := map[string][]byte{}
	for _, path := range []string{filepath.Join(paths[0], "unrelated-tool"), filepath.Join(paths[0], "other-tool"), filepath.Join(paths[1], "other-skill", "SKILL.md"), filepath.Join(paths[3], "other-host.json"), filepath.Join(paths[4], "simple", "machine.key"), filepath.Join(paths[4], "simple", "credentials.json"), filepath.Join(paths[4], "simple", "known_hosts", "pin"), filepath.Join(paths[4], "user-backup.json"), filepath.Join(paths[4], "unrelated"), filepath.Join(root, ".zshrc")} {
		preserved[path] = []byte("preserve:" + filepath.Base(path))
		files = append(files, path)
	}
	for _, path := range files {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		content := []byte("installed")
		if p, ok := preserved[path]; ok {
			content = p
		}
		if e := os.WriteFile(path, content, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return paths, preserved
}
func runUninstallFixture(t *testing.T, paths []string) {
	t.Helper()
	if out, e := uninstallShell(t, `source "$1"; shift; uninstall_paths "$@"`, paths...); e != nil {
		t.Fatal(e, string(out))
	}
}
func TestUninstallPreciseRemovalPreservationAndIdempotence(t *testing.T) {
	paths, preserved := fixture(t)
	runUninstallFixture(t, paths)
	runUninstallFixture(t, paths)
	for _, path := range []string{filepath.Join(paths[0], "xssh"), filepath.Join(paths[0], "xweb"), filepath.Join(paths[1], "accessctl-ssh"), filepath.Join(paths[1], "accessctl-web"), paths[2], filepath.Join(paths[3], "com.x_gateway.accessctl.json"), filepath.Join(paths[4], "xweb-native-host.sh")} {
		if _, e := os.Lstat(path); !os.IsNotExist(e) {
			t.Fatal("installed target remains")
		}
	}
	for path, want := range preserved {
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("preserved data modified")
		}
	}
	for _, parent := range []string{paths[0], paths[1], filepath.Dir(paths[2]), paths[3], paths[4]} {
		info, e := os.Stat(parent)
		if e != nil || !info.IsDir() {
			t.Fatal("shared parent removed")
		}
	}
}
func TestUninstallSymlinkLeavesAndNestedLinksDoNotFollow(t *testing.T) {
	paths, _ := fixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	os.MkdirAll(outside, 0700)
	sentinel := filepath.Join(outside, "keep")
	os.WriteFile(sentinel, []byte("untouched"), 0600)
	for _, path := range []string{filepath.Join(paths[0], "xssh"), filepath.Join(paths[1], "accessctl-ssh"), paths[2]} {
		os.RemoveAll(path)
		if e := os.Symlink(outside, path); e != nil {
			t.Fatal(e)
		}
	}
	os.Remove(filepath.Join(paths[0], "xweb"))
	os.Symlink(filepath.Join(outside, "missing"), filepath.Join(paths[0], "xweb"))
	os.Symlink(outside, filepath.Join(paths[1], "accessctl-web", "nested-link"))
	runUninstallFixture(t, paths)
	got, e := os.ReadFile(sentinel)
	if e != nil || string(got) != "untouched" {
		t.Fatal("symlink destination followed")
	}
}
func TestUninstallPreflightRejectsUnsafeRootsAndDirectoryAtFile(t *testing.T) {
	paths, _ := fixture(t)
	shim := filepath.Join(paths[4], "xweb-native-host.sh")
	os.Remove(shim)
	os.Mkdir(shim, 0700)
	if _, e := uninstallShell(t, `source "$1"; shift; uninstall_paths "$@"`, paths...); e == nil {
		t.Fatal("directory at file position accepted")
	}
	if _, e := os.Stat(filepath.Join(paths[0], "xssh")); e != nil {
		t.Fatal("preflight deleted before later error")
	}
	for _, root := range []string{"", "relative", "/", "/tmp/../danger"} {
		bad := append([]string{}, paths...)
		bad[0] = root
		if _, e := uninstallShell(t, `source "$1"; shift; uninstall_paths "$@"`, bad...); e == nil {
			t.Fatal("unsafe root accepted")
		}
	}
}
func TestUninstallHelpAndInvalidArgumentsNeverReachDeletion(t *testing.T) {
	path := uninstaller(t)
	if out, e := exec.Command("bash", "-n", path).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	marker := filepath.Join(t.TempDir(), "called")
	var previous []byte
	for _, arg := range []string{"-h", "--help", "--purge", "--unknown"} {
		out, e := uninstallShell(t, `source "$1"; marker="$2"; uninstall_paths() { printf called > "$marker"; }; main "$3"`, marker, arg)
		// The function override would write only the test marker if main ever
		// reached removal. HOME retains its real value.
		if arg == "-h" || arg == "--help" {
			if e != nil {
				t.Fatal(e, string(out))
			}
			if previous != nil && !bytes.Equal(previous, out) {
				t.Fatal("help aliases differ")
			}
			previous = out
		} else if e == nil {
			t.Fatal("unknown option accepted")
		}
		if _, e := os.Stat(marker); !os.IsNotExist(e) {
			t.Fatal("help/invalid option reached deletion")
		}
	}
}

func TestUninstallUsesUniversalSkillsAndAlwaysPlansBrowserCleanup(t *testing.T) {
	for _, platform := range []string{"Darwin", "Linux"} {
		audit := filepath.Join(t.TempDir(), "audit")
		script := `source "$1"
test_platform="$2"; audit="$3"; DISPLAY=''; WAYLAND_DISPLAY=''
uname() { printf '%s\n' "$test_platform"; }
uninstall_paths() { printf '%s\n' "$@" > "$audit"; }
main
`
		if out, e := uninstallShell(t, script, platform, audit); e != nil {
			t.Fatal(e, string(out))
		}
		b, e := os.ReadFile(audit)
		if e != nil {
			t.Fatal(e)
		}
		lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
		if len(lines) != 5 || string(lines[1]) != filepath.Join(os.Getenv("HOME"), ".agents", "skills") || string(lines[2]) != filepath.Join(os.Getenv("HOME"), ".local", "share", "x-gateway", "extension") {
			t.Fatal("uninstall skills or browser scope changed")
		}
		if !bytes.Contains(lines[3], []byte("NativeMessagingHosts")) || !bytes.Contains(lines[4], []byte("x-gateway")) {
			t.Fatal("headless uninstall skipped browser/shim scope")
		}
	}
}
