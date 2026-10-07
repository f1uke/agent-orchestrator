package simhealth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simproc"
)

const (
	udidBooted   = "AAAAAAAA-0000-0000-0000-000000000001"
	udidShutdown = "BBBBBBBB-0000-0000-0000-000000000002"
	udidOther    = "CCCCCCCC-0000-0000-0000-000000000003"
	appID        = "com.example.app"
	session      = domain.SessionID("proj-7")
)

// fixture is one machine: three simulators whose data directories are real
// folders, and readers over them that a test overrides one at a time.
type fixture struct {
	data    map[string]string // udid -> data path
	readers Readers
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{data: map[string]string{}}
	devices := []simctl.Device{
		{UDID: udidBooted, Name: "iPhone 17", State: simctl.BootedState},
		{UDID: udidShutdown, Name: "iPhone 16e", State: "Shutdown"},
		{UDID: udidOther, Name: "iPad Air", State: simctl.BootedState},
	}
	for i := range devices {
		devices[i].DataPath = t.TempDir()
		f.data[devices[i].UDID] = devices[i].DataPath
	}
	f.readers = Readers{
		Devices:   func(context.Context) ([]simctl.Device, error) { return devices, nil },
		Assigned:  func(context.Context, domain.SessionID) (string, error) { return "", nil },
		Leases:    func(context.Context) ([]domain.SimLease, error) { return nil, nil },
		CAFiles:   func(context.Context, domain.SessionID) ([]string, error) { return nil, nil },
		Run:       unsignedRunner,
		Trusted:   TrustStoreHas,
		Processes: func(context.Context) (simproc.Table, error) { return nil, nil },
	}
	return f
}

// unsignedRunner answers plutil by reading the fixture's Info.plist, which the
// fixtures write as JSON already, and codesign as an unsigned bundle - so
// simbuild falls back to hashing the bundle's real bytes, and two bundles get
// the same digest exactly when their files match.
func unsignedRunner(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "plutil":
		return os.ReadFile(args[len(args)-1]) //nolint:gosec // test fixture
	case "codesign":
		return []byte("code object is not signed at all"), errors.New("exit 1")
	}
	return nil, errors.New("unexpected command " + name)
}

// writeBundle lays out a .app at dir/<name>.app with an Info.plist (as JSON)
// and one executable whose content tells builds apart.
func writeBundle(t *testing.T, dir, bundleID, version, number, binary string) string {
	t.Helper()
	app := filepath.Join(dir, "App.app")
	if err := os.MkdirAll(app, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := json.Marshal(map[string]string{
		"CFBundleIdentifier": bundleID, "CFBundleShortVersionString": version, "CFBundleVersion": number,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"Info.plist": info, "App": []byte(binary)} {
		if err := os.WriteFile(filepath.Join(app, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

// install puts a build on a device the way CoreSimulator lays one out.
func (f *fixture) install(t *testing.T, udid, bundleID, version, number, binary string) {
	t.Helper()
	writeBundle(t, filepath.Join(f.data[udid], "Containers", "Bundle", "Application", "C0FFEE"), bundleID, version, number, binary)
}

// newCA writes a self-signed root to dir/name, PEM or DER, and returns its
// path and the SHA-256 of its DER - the key the device's trust store uses.
func newCA(t *testing.T, dir, name string, asPEM bool) (string, [sha256.Size]byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	content := der
	if asPEM {
		content = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, sha256.Sum256(der)
}

// trust writes a device trust store holding these roots, shaped like the one
// `simctl keychain add-root-cert` writes.
func (f *fixture) trust(t *testing.T, udid string, sums ...[sha256.Size]byte) string {
	t.Helper()
	store := TrustStorePath(f.data[udid])
	if err := os.MkdirAll(filepath.Dir(store), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE tsettings (sha256 BLOB NOT NULL DEFAULT '', subj BLOB NOT NULL DEFAULT '', tset BLOB, data BLOB, PRIMARY KEY(sha256))"); err != nil {
		t.Fatal(err)
	}
	for _, sum := range sums {
		if _, err := db.Exec("INSERT INTO tsettings (sha256) VALUES (?)", sum[:]); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func line(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("report has no %q line: %+v", name, r.Checks)
	return Check{}
}

func expect(t *testing.T, c Check, status Status, contains ...string) {
	t.Helper()
	if c.Status != status {
		t.Errorf("%s = %s %q, want %s", c.Name, c.Status, c.Message, status)
	}
	for _, want := range contains {
		if !strings.Contains(c.Message, want) {
			t.Errorf("%s message %q does not contain %q", c.Name, c.Message, want)
		}
	}
}

func TestDevice_NoneNamedAndNoneAssignedFailsAloneAndListsTheBootedOnes(t *testing.T) {
	f := newFixture(t)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session})
	if len(r.Checks) != 1 || r.OK {
		t.Fatalf("a report about no device must be that one failing line, got %+v", r)
	}
	expect(t, r.Checks[0], StatusFail, "pass --udid", "$AO_SIM_UDID", "iPhone 17 "+udidBooted, "iPad Air "+udidOther)
	if strings.Contains(r.Checks[0].Message, udidShutdown) {
		t.Errorf("a shut-down device is listed as booted: %q", r.Checks[0].Message)
	}
}

func TestDevice_UnknownUDIDFailsAlone(t *testing.T) {
	f := newFixture(t)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: "dddddddd-0000-0000-0000-000000000004"})
	if len(r.Checks) != 1 {
		t.Fatalf("an unknown device leaves nothing else to check, got %+v", r.Checks)
	}
	expect(t, r.Checks[0], StatusFail, "DDDDDDDD-0000-0000-0000-000000000004: no such simulator")
}

func TestDevice_NotBootedFailsWithTheBootCommandAndStillReadsTheDisk(t *testing.T) {
	f := newFixture(t)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidShutdown})
	expect(t, line(t, r, CheckDevice), StatusFail, "iPhone 16e ("+udidShutdown+") is Shutdown", "`ao sim boot --udid "+udidShutdown+"`")
	if len(r.Checks) != 5 {
		t.Errorf("a shut-down device's installs and trust store are still on disk, so every line applies; got %+v", r.Checks)
	}
	if r.OK {
		t.Error("a report with a FAIL line is not OK")
	}
}

func TestDevice_NamingAnotherThanTheAssignedOneWarns(t *testing.T) {
	f := newFixture(t)
	f.readers.Assigned = func(context.Context, domain.SessionID) (string, error) { return strings.ToLower(udidBooted), nil }
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidOther})
	expect(t, line(t, r, CheckDevice), StatusWarn, "iPad Air ("+udidOther+") booted", "this session's device is "+udidBooted)
}

func TestDevice_TheAssignedOneIsTheDefaultAndOK(t *testing.T) {
	f := newFixture(t)
	f.readers.Assigned = func(context.Context, domain.SessionID) (string, error) { return udidBooted, nil }
	for _, udid := range []string{"", strings.ToLower(udidBooted)} {
		r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udid})
		expect(t, line(t, r, CheckDevice), StatusOK, "iPhone 17 ("+udidBooted+") booted")
	}
}

func TestDevice_AListingThatFailsIsAFailure(t *testing.T) {
	f := newFixture(t)
	f.readers.Devices = func(context.Context) ([]simctl.Device, error) { return nil, errors.New("xcrun not found") }
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckDevice), StatusFail, "could not list simulators: xcrun not found")
}

func TestLease(t *testing.T) {
	expires := time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)
	other := &domain.SimDaemon{DataDir: "/tmp/sandbox/data", PID: 4242, Port: 3399}
	cases := []struct {
		name     string
		leases   []domain.SimLease
		status   Status
		contains []string
	}{
		{"unheld warns", nil, StatusWarn, []string{"no AO session holds it", "a run will claim it"}},
		{"another device's lease is not this one's", []domain.SimLease{{UDID: udidOther, SessionID: "proj-9", ExpiresAt: expires}}, StatusWarn, []string{"no AO session holds it"}},
		{"held by this session", []domain.SimLease{{UDID: strings.ToLower(udidBooted), SessionID: session, ExpiresAt: expires}}, StatusOK, []string{"held by this session until 2026-10-07T09:30:00Z"}},
		{"held by another session", []domain.SimLease{{UDID: udidBooted, SessionID: "proj-9", ExpiresAt: expires}}, StatusFail, []string{"held by @proj-9 until 2026-10-07T09:30:00Z"}},
		// The same session id through another daemon is somebody else.
		{"held through another daemon", []domain.SimLease{{UDID: udidBooted, SessionID: session, ExpiresAt: expires, OtherDaemon: other}}, StatusFail, []string{"held by @proj-7 through " + other.Describe()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.readers.Leases = func(context.Context) ([]domain.SimLease, error) { return tc.leases, nil }
			r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
			expect(t, line(t, r, CheckLease), tc.status, tc.contains...)
			if r.OK != (tc.status != StatusFail) {
				t.Errorf("report OK = %v with a %s lease line", r.OK, tc.status)
			}
		})
	}
}

func TestApp_NoBundleIDWarnsThatItWasNotChecked(t *testing.T) {
	f := newFixture(t)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckApp), StatusWarn, "not checked", "--app")
}

func TestApp_NotInstalledFails(t *testing.T) {
	f := newFixture(t)
	f.install(t, udidBooted, "com.example.other", "1.0", "1", "other")
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted, App: appID})
	expect(t, line(t, r, CheckApp), StatusFail, appID+" is not installed")
}

func TestApp_InstalledWithNothingToCompareWarnsWithTheBuild(t *testing.T) {
	f := newFixture(t)
	f.install(t, udidBooted, appID, "6.5.18", "708", "build one")
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted, App: appID})
	expect(t, line(t, r, CheckApp), StatusWarn, appID+" 6.5.18 (708) sha256:", "--expect")
}

func TestApp_ExpectComparesDigestsMadeTheSameWay(t *testing.T) {
	cases := []struct {
		name     string
		bundleID string
		binary   string
		status   Status
		contains string
	}{
		{"same bytes", appID, "build one", StatusOK, "is the build at"},
		{"other bytes", appID, "build two", StatusFail, "is not the build at"},
		{"other app", "com.example.other", "build one", StatusFail, "is com.example.other, not " + appID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.install(t, udidBooted, appID, "6.5.18", "708", "build one")
			want := writeBundle(t, t.TempDir(), tc.bundleID, "6.5.18", "708", tc.binary)
			r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted, App: appID, Expect: want})
			expect(t, line(t, r, CheckApp), tc.status, tc.contains)
		})
	}
}

// running is a process table holding these `ps` rows.
func running(rows ...string) func(context.Context) (simproc.Table, error) {
	return func(context.Context) (simproc.Table, error) {
		return simproc.Parse([]byte(strings.Join(rows, "\n"))), nil
	}
}

func appRow(pid, ppid int, stat, dataPath, inBundle string) string {
	return fmt.Sprintf("%d %d %s %s/Containers/Bundle/Application/C0FFEE/%s", pid, ppid, stat, dataPath, inBundle)
}

func TestDebugger_AnAppAttachedOnThisDeviceFailsNamingTheChainAndTheFix(t *testing.T) {
	f := newFixture(t)
	f.readers.Processes = running(
		"900 880 S+ /usr/bin/lldb",
		"950 900 S /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
		appRow(601, 950, "SXs", f.data[udidBooted], "Nimbus.app/Nimbus"),
	)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckDebugger), StatusFail,
		"Nimbus.app/Nimbus (pid 601)", "debugserver 950 (lldb 900)", "`process detach`", "end that lldb (pid 900)")
	if r.OK {
		t.Error("a frozen app is a failing report")
	}
}

func TestDebugger_ASIGSTOPdExtensionFailsToo(t *testing.T) {
	f := newFixture(t)
	f.readers.Processes = running(
		appRow(601, 500, "Ss", f.data[udidBooted], "Nimbus.app/Nimbus"),
		appRow(602, 500, "T", f.data[udidBooted], "Nimbus.app/PlugIns/Widget.appex/Widget"),
	)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	c := line(t, r, CheckDebugger)
	expect(t, c, StatusFail, "Widget.appex/Widget (pid 602) is stopped by SIGSTOP", "`kill -CONT 602`")
	if strings.Contains(c.Message, "pid 601") {
		t.Errorf("the running app is reported as held: %q", c.Message)
	}
}

func TestDebugger_AnotherDevicesHeldAppIsNotThisOnesProblem(t *testing.T) {
	f := newFixture(t)
	f.readers.Processes = running(
		appRow(601, 500, "Ss", f.data[udidBooted], "Nimbus.app/Nimbus"),
		appRow(701, 950, "SXs", f.data[udidOther], "Nimbus.app/Nimbus"),
	)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckDebugger), StatusOK, "no app on this device is held by a debugger")
}

func TestDebugger_AShutDownDeviceRunsNothing(t *testing.T) {
	f := newFixture(t)
	f.readers.Processes = func(context.Context) (simproc.Table, error) {
		t.Error("the process table was read for a device that is not booted")
		return nil, nil
	}
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidShutdown})
	expect(t, line(t, r, CheckDebugger), StatusOK, "not booted")
}

func TestDebugger_AnUnreadableProcessTableFails(t *testing.T) {
	f := newFixture(t)
	f.readers.Processes = func(context.Context) (simproc.Table, error) { return nil, errors.New("ps: boom") }
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckDebugger), StatusFail, "could not read", "ps: boom")
}

func TestProxyCA_NothingConfiguredOrPresentWarns(t *testing.T) {
	f := newFixture(t)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusWarn, "no root CA is configured")

	missing := filepath.Join(t.TempDir(), "proxyman-ca.pem")
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) { return []string{missing}, nil }
	r = Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusWarn, "none of the configured root CAs is on this Mac", missing)
}

func TestProxyCA_EveryPresentCATrustedIsOK(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	pemPath, pemSum := newCA(t, dir, "proxyman-ca.pem", true)
	derPath, derSum := newCA(t, dir, "charles.cer", false)
	absent := filepath.Join(dir, "gone.pem")
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) {
		return []string{pemPath, absent, derPath}, nil
	}
	f.trust(t, udidBooted, pemSum, derSum)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusOK, pemPath+", "+derPath+" trusted on this simulator")
}

func TestProxyCA_APresentCAMissingFromTheTrustStoreFails(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	trustedPath, trustedSum := newCA(t, dir, "trusted.pem", true)
	missingPath, _ := newCA(t, dir, "proxyman-ca.pem", true)
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) {
		return []string{trustedPath, missingPath}, nil
	}
	f.trust(t, udidBooted, trustedSum)
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusFail, missingPath+" not trusted", "`ao sim claim` trusts it")
	if strings.Contains(line(t, r, CheckProxyCA).Message, trustedPath) {
		t.Errorf("the trusted CA is reported as missing: %q", line(t, r, CheckProxyCA).Message)
	}
}

func TestProxyCA_ADeviceWithNoTrustStoreTrustsNothing(t *testing.T) {
	f := newFixture(t)
	path, _ := newCA(t, t.TempDir(), "proxyman-ca.pem", true)
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) { return []string{path}, nil }
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusFail, path+" not trusted")
}

func TestProxyCA_UnresolvableSettingFails(t *testing.T) {
	f := newFixture(t)
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) { return nil, errors.New("project gone") }
	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted})
	expect(t, line(t, r, CheckProxyCA), StatusFail, "could not work out which root CAs", "project gone")
}

// The doctor reports; it never repairs. Every command it runs goes through the
// injected runner, so recording them proves which ones it ran: listing the
// devices and reading two bundles, nothing that boots, claims, installs or
// trusts. The trust store, which trustd owns, must come out byte for byte
// what it went in as.
func TestDiagnose_RunsOnlyReadCommandsAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.install(t, udidBooted, appID, "6.5.18", "708", "build one")
	want := writeBundle(t, t.TempDir(), appID, "6.5.18", "708", "build two")
	caPath, caSum := newCA(t, t.TempDir(), "proxyman-ca.pem", true)
	store := f.trust(t, udidBooted)
	before, err := os.ReadFile(store) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}

	listing, err := json.Marshal(map[string]any{"devices": map[string]any{
		"com.apple.CoreSimulator.SimRuntime.iOS-26-3": []map[string]any{
			{"udid": udidBooted, "name": "iPhone 17", "state": "Booted", "isAvailable": true, "dataPath": f.data[udidBooted]},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var ran [][]string
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, append([]string{name}, args...))
		if name == simctl.Binary {
			return listing, nil
		}
		return unsignedRunner(ctx, name, args...)
	}
	f.readers.Devices = func(ctx context.Context) ([]simctl.Device, error) {
		return simctl.List(ctx, func(string) (string, error) { return "/usr/bin/xcrun", nil }, run)
	}
	f.readers.Run = run
	f.readers.CAFiles = func(context.Context, domain.SessionID) ([]string, error) { return []string{caPath}, nil }

	r := Diagnose(t.Context(), f.readers, Request{SessionID: session, UDID: udidBooted, App: appID, Expect: want})
	if len(r.Checks) != 5 || r.OK {
		t.Fatalf("expected five lines with the app and CA failing, got %+v", r)
	}

	reads := [][]string{
		{simctl.Binary, "simctl", "list", "devices", "--json"},
		{"plutil", "-convert", "json", "-o", "-"},
		{"codesign", "-d"},
	}
	for _, cmd := range ran {
		allowed := false
		for _, prefix := range reads {
			if len(cmd) >= len(prefix) && strings.Join(cmd[:len(prefix)], " ") == strings.Join(prefix, " ") {
				allowed = true
			}
		}
		if !allowed {
			t.Errorf("the doctor ran %q, which is not a read", strings.Join(cmd, " "))
		}
	}
	if len(ran) == 0 {
		t.Fatal("no command was recorded, so this test proves nothing")
	}

	after, err := os.ReadFile(store) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the device's trust store changed during a doctor run")
	}
	if has, err := TrustStoreHas(t.Context(), store, caSum); err != nil || has {
		t.Errorf("the CA the doctor found missing is now trusted (has=%v, err=%v): it must report, never install", has, err)
	}
}
