package subscription

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRefreshFailure_UnclassifiedCauseKeepsSafeInternalContract(t *testing.T) {
	for _, condition := range []string{"status write fails", "profile removed"} {
		t.Run(condition, func(t *testing.T) {
			service, url := newServiceForTest(t, http.NotFoundHandler())
			profile, err := service.Add("fixture", url, "")
			if err != nil {
				t.Fatal(err)
			}
			id := profile.ID
			if condition == "status write fails" {
				service.catalogPath = t.TempDir() // A regular catalog cannot replace a directory.
			} else {
				id = "removed-profile"
			}
			before := service.Snapshot()
			cause := errors.New("private network detail token=fixture")
			err = service.refreshFailure(id, cause)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInternal || api.Message != "internal error" || err.Error() != "internal error" {
				t.Fatal("secondary failure changed the original public fallback or exposed private text")
			}
			if !errors.Is(err, cause) {
				t.Fatal("original cause lost")
			}
			if condition == "status write fails" {
				var link *os.LinkError
				var path *os.PathError
				var errno syscall.Errno
				if !errors.As(err, &link) && !errors.As(err, &path) && !errors.As(err, &errno) {
					t.Fatal("status write cause lost")
				}
			}
			if !reflect.DeepEqual(service.Snapshot(), before) {
				t.Fatal("failed status update changed catalog state")
			}
		})
	}
}
