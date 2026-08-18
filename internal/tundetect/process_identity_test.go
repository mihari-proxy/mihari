package tundetect

import "testing"

func TestParseLinuxStatPPID_SimpleComm(t *testing.T) {
	got, ok := parseLinuxStatPPID("43560 (mihomo) S 36560 36560 36560 0 -1")
	if !ok || got != 36560 {
		t.Fatalf("got=%d ok=%v", got, ok)
	}
}

func TestParseLinuxStatPPID_CommWithSpaces(t *testing.T) {
	got, ok := parseLinuxStatPPID("10 (mihomo extra) R 1 2 3")
	if !ok || got != 1 {
		t.Fatalf("got=%d ok=%v", got, ok)
	}
}

func TestParseLinuxStatPPID_RejectsMalformed(t *testing.T) {
	if _, ok := parseLinuxStatPPID("no-paren S 1"); ok {
		t.Fatal("expected reject")
	}
	if _, ok := parseLinuxStatPPID("1 (mihomo) S"); ok {
		t.Fatal("expected reject missing ppid")
	}
}

func TestParseDarwinProcessLine(t *testing.T) {
	name, pid, ppid, ok := parseDarwinProcessLine("/usr/local/bin/mihomo 43560 36560")
	if !ok || name != "/usr/local/bin/mihomo" || pid != 43560 || ppid != 36560 {
		t.Fatalf("name=%q pid=%d ppid=%d ok=%v", name, pid, ppid, ok)
	}
}

func TestParseDarwinProcessLine_RejectsShort(t *testing.T) {
	if _, _, _, ok := parseDarwinProcessLine("mihomo 123"); ok {
		t.Fatal("expected reject")
	}
}
