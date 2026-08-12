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

func FuzzParseUserInfo(f *testing.F) {
	seeds := []string{
		"",
		"upload=100; download=200; total=1000; expire=1710000000",
		"Upload = 1 ; Download = 2",
		"upload=1; upload=2; download=3",
		"upload=-1; download=5",
		"upload=9223372036854775807; download=1",
		"upload=999999999999999999999",
		"garbage;;;=;;",
		"unknown=7; total=9",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		info, ok := ParseUserInfo(raw)
		if !ok {
			if info != (UserInfo{}) {
				t.Fatalf("ok=false with non-zero info: %#v", info)
			}
			return
		}
		if info.Upload < 0 || info.Download < 0 || info.Total < 0 || info.Expire < 0 {
			t.Fatalf("negative field: %#v", info)
		}
		if info.Used() < 0 {
			t.Fatalf("Used() negative: %d", info.Used())
		}
	})
}
