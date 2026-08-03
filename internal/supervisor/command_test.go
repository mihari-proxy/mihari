package supervisor

import (
	"reflect"
	"testing"
)

func TestCommandArgumentsUseManagedDataAndConfig(t *testing.T) {
	want := []string{"-d", "/managed/data", "-f", "/managed/config.yaml"}
	if got := commandArguments("/managed/data", "/managed/config.yaml"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}
