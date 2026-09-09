package service

import (
	"context"
	"errors"
	"golang.org/x/sys/windows/svc/mgr"
	"testing"
)

func TestServiceReplacement_DefinitionFingerprint(t *testing.T) {
	config := mgr.Config{BinaryPathName: `"C:\Program Files\Mihari\mihari.exe" daemon`, StartType: 2, ServiceStartName: "LocalSystem"}
	first, err := windowsReplacementServiceView(context.Background(), &config, `C:\Program Files\Mihari\mihari.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Registered || len(first.DefinitionSHA256) != 64 {
		t.Fatal(first)
	}
	changed := config
	changed.BinaryPathName += " --data alternate"
	next, err := windowsReplacementServiceView(context.Background(), &changed, first.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.DefinitionSHA256 == next.DefinitionSHA256 {
		t.Fatal("changed service command was not bound")
	}
	absent, err := windowsReplacementServiceView(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if absent.Registered || absent.DefinitionSHA256 == first.DefinitionSHA256 {
		t.Fatal("absent registration was not bound")
	}
}

func TestServiceReplacement_CancelledMissingServiceView(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	view, err := windowsReplacementServiceView(ctx, nil, "")
	if !errors.Is(err, context.Canceled) || view != (ServiceReplacementView{}) {
		t.Fatalf("cancelled SCM result accepted: view=%+v err=%v", view, err)
	}
}
