package logging

import (
	"context"
	"testing"
)

func TestOperationContext_ValueAndInheritance(t *testing.T) {
	input := OperationMetadata{ID: "op-a", Name: "settings.update"}
	parent := WithOperation(context.Background(), input)
	input.ID = "changed"
	child, cancel := context.WithCancel(parent)
	cancel()
	got, ok := OperationFromContext(child)
	if !ok || got.ID != "op-a" || got.Name != "settings.update" {
		t.Fatal("bound metadata must survive input mutation and cancellation")
	}
	got.ID = "edited-copy"
	original, _ := OperationFromContext(parent)
	if original.ID != "op-a" || child.Err() != context.Canceled {
		t.Fatal("metadata retrieval must not mutate parent or cancellation")
	}
	replacement := WithOperation(parent, OperationMetadata{ID: "op-b"})
	second, _ := OperationFromContext(replacement)
	original, _ = OperationFromContext(parent)
	if second.ID != "op-b" || original.ID != "op-a" {
		t.Fatal("replacement must be local to the derived context")
	}
}

func TestOperationContext_AbsentAndExplicitEmpty(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		if got, ok := OperationFromContext(ctx); ok || got != (OperationMetadata{}) {
			t.Fatal("unbound context must have no operation")
		}
	}
	parent := WithOperation(context.Background(), OperationMetadata{ID: "parent"})
	empty := WithOperation(parent, OperationMetadata{})
	if got, ok := OperationFromContext(empty); !ok || got != (OperationMetadata{}) {
		t.Fatal("explicit empty binding must mask the parent")
	}
	raw := "not valid/" + string([]byte{0})
	ctx := WithOperation(parent, OperationMetadata{ID: raw})
	if got, _ := OperationFromContext(ctx); got.ID != raw {
		t.Fatal("context binding must not rewrite input")
	}
}
