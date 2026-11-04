package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestReadUpdateContent_ReadAndCancellationErrorsWinOverSize(t *testing.T) {
	cause := errors.New("partial executable read failed")
	for _, retain := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			readErr error
			cancel  bool
			want    error
		}{
			{name: "read failure", readErr: cause, want: cause},
			{name: "read failure with cancellation", readErr: cause, cancel: true, want: cause},
			{name: "canceled at limit", cancel: true, want: context.Canceled},
			{name: "oversize only", want: os.ErrInvalid},
		} {
			t.Run(fmt.Sprintf("retain=%v/%s", retain, tc.name), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reader := &digestTestReader{read: func(b []byte) (int, error) {
					if tc.cancel {
						cancel()
					}
					return len(b), tc.readErr
				}}
				_, _, size, err := readUpdateContent(ctx, reader, retain, 8)
				if size != 9 || !errors.Is(err, tc.want) {
					t.Fatalf("size=%d err=%v, want %v", size, err, tc.want)
				}
			})
		}
	}
}
