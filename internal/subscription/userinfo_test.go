package subscription

import "testing"

func TestParseUserInfo(t *testing.T) {
	info, ok := ParseUserInfo("upload=100; download=200; total=1000; expire=1710000000")
	if !ok {
		t.Fatal("expected ok")
	}
	if info.Upload != 100 || info.Download != 200 || info.Total != 1000 || info.Expire != 1710000000 {
		t.Fatalf("%#v", info)
	}
	if info.Used() != 300 {
		t.Fatalf("used=%d", info.Used())
	}
	if _, ok := ParseUserInfo(""); ok {
		t.Fatal("empty should fail")
	}
	if _, ok := ParseUserInfo("not-a-header"); ok {
		t.Fatal("garbage should fail")
	}
}
