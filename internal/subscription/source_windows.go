package subscription

import "os"

func openSourceFile(path string) (*os.File, error) { return os.Open(path) }
